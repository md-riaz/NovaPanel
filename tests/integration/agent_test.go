package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	runner "novapanel/internal/exec"
)

func TestScratchAgent(t *testing.T) {
	if os.Getenv("NOVAPANEL_INTEGRATION") != "1" {
		t.Skip("set NOVAPANEL_INTEGRATION=1 for real root-only scratch integration")
	}
	if os.Geteuid() != 0 {
		t.Fatal("integration requires root in a disposable runner")
	}
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	scratch, err := os.MkdirTemp("", "novapanel-integration-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(scratch)
	if err := os.Chmod(scratch, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(prog string, args []string, opts runner.RunOpts) (string, int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		out, stderr, exit, err := (runner.Real{}).Run(ctx, prog, args, opts)
		text := string(out) + string(stderr)
		if err != nil && exit < 0 {
			t.Fatalf("run %s: %v %s", prog, err, text)
		}
		return text, exit
	}
	goBin := "/usr/bin/env" // Resolve Go from PATH without importing os/exec.
	for _, name := range []string{"panel-agent", "novapanelctl", "panel-web"} {
		if text, exit := run(goBin, []string{"go", "build", "-o", filepath.Join(scratch, name), "./cmd/" + name}, runner.RunOpts{Dir: root, Env: os.Environ()}); exit != 0 {
			t.Fatalf("build %s: %s", name, text)
		}
	}
	socket := filepath.Join(scratch, "agent.sock")
	config := filepath.Join(scratch, "agent.toml")
	if err := os.WriteFile(config, []byte(fmt.Sprintf("socket_path = %q\nweb_user = \"nobody\"\n", socket)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		text string
		exit int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		out, stderr, exit, err := (runner.Real{}).Run(ctx, filepath.Join(scratch, "panel-agent"), []string{"--config", config}, runner.RunOpts{Dir: scratch, Env: os.Environ()})
		done <- result{string(out) + string(stderr), exit, err}
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		select {
		case r := <-done:
			done <- r
			t.Fatalf("agent stopped before socket: %+v", r)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("socket startup timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, verb := range []string{"ping", "agent.info"} {
		text, exit := run(filepath.Join(scratch, "novapanelctl"), []string{"--socket", socket, verb}, runner.RunOpts{Dir: scratch, Env: os.Environ()})
		if exit != 0 {
			t.Fatalf("root %s: exit=%d %s", verb, exit, text)
		}
		var reply struct {
			OK   bool
			Data json.RawMessage
		}
		if err := json.Unmarshal([]byte(text), &reply); err != nil || !reply.OK || len(reply.Data) == 0 {
			t.Fatalf("root %s invalid reply: %s", verb, text)
		}
	}
	if text, exit := run(filepath.Join(scratch, "panel-web"), nil, runner.RunOpts{Dir: scratch}); exit == 0 || !strings.Contains(strings.ToLower(text), "root") {
		t.Fatalf("root web not refused: %d %s", exit, text)
	}
	// The probe starts as root and changes directory before dropping credentials.
	// This keeps all files under scratch without making /root ancestors traversable.
	// The socket's configured gid grants access to BOTH test uids, so
	// rejection cannot be mistaken for a filesystem EACCES failure.
	probe := `import json,os,socket,sys
uid=int(sys.argv[1]); verb=sys.argv[2]
os.setgroups([]); os.setgid(os.stat("agent.sock").st_gid); os.setuid(uid)
s=socket.socket(socket.AF_UNIX); s.settimeout(3)
s.connect("agent.sock")
print("CONNECTED", flush=True)
try:
 s.sendall((json.dumps({"id":"probe","verb":verb,"args":{},"actor":"owner"})+"\n").encode())
 data=s.recv(4096)
 print(data.decode() if data else "PEER_CLOSED")
except (ConnectionResetError,BrokenPipeError): print("PEER_CLOSED")
`
	python := "/usr/bin/python3"
	for _, tc := range []struct{ uid, verb, want string }{{"65534", "ping", `"ok":true`}, {"65534", "agent.info", `"code":"unauthenticated"`}, {"65533", "ping", "PEER_CLOSED"}} {
		text, exit := run(python, []string{"-c", probe, tc.uid, tc.verb}, runner.RunOpts{Dir: scratch})
		compact := strings.ReplaceAll(text, " ", "")
		if exit != 0 || !strings.Contains(text, "CONNECTED") || !strings.Contains(compact, tc.want) {
			t.Fatalf("uid=%s %s: exit=%d %s", tc.uid, tc.verb, exit, text)
		}
	}
	t.Log("real binaries: root ping/info accepted; root web refused; web ping accepted and owner info denied; disallowed uid connected through filesystem then rejected by SO_PEERCRED")
}

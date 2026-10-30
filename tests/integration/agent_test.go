package integration

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"novapanel/internal/config"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
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
	for _, name := range []string{"novapanelctl", "panel-web"} {
		if text, exit := run(goBin, []string{"go", "build", "-o", filepath.Join(scratch, name), "./cmd/" + name}, runner.RunOpts{Dir: root, Env: os.Environ()}); exit != 0 {
			t.Fatalf("build %s: %s", name, text)
		}
	}
	// Reuse the production lifecycle with its test-only injected runner, never exec.Real.
	if text, exit := run(goBin, []string{"go", "test", "-c", "-o", filepath.Join(scratch, "panel-agent.test"), "./cmd/panel-agent"}, runner.RunOpts{Dir: root, Env: os.Environ()}); exit != 0 {
		t.Fatalf("build safe daemon: %s", text)
	}
	socket := filepath.Join(scratch, "agent.sock")
	cfg := config.Defaults()
	cfg.SocketPath = socket
	cfg.DBPath = filepath.Join(scratch, "state", "panel.db")
	cfg.SealKeyPath = filepath.Join(scratch, "state", "seal.key")
	cfg.LogDir = filepath.Join(scratch, "log")
	cfg.ACMEWebroot = filepath.Join(scratch, "acme")
	cfg.SitesRoot = filepath.Join(scratch, "sites")
	cfg.TLSDir = filepath.Join(scratch, "tls")
	cfg.WebUID, cfg.WebGID = 65534, 65534
	encodedConfig, err := json.Marshal(cfg)
	if err != nil {
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
	start := func() {
		go func() {
			out, stderr, exit, err := (runner.Real{}).Run(ctx, filepath.Join(scratch, "panel-agent.test"), []string{"-test.run=^TestSafeDaemonHelper$"}, runner.RunOpts{Dir: scratch, Env: append(os.Environ(), "NOVAPANEL_SAFE_HELPER=1", "NOVAPANEL_SAFE_CONFIG="+string(encodedConfig))})
			done <- result{string(out) + string(stderr), exit, err}
		}()
	}
	start()
	running := true
	defer func() {
		cancel()
		if !running {
			return
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
	}()
	waitSocket := func() {
		t.Helper()
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
	}
	waitSocket()
	for _, verb := range []string{"ping", "agent.info"} {
		text, exit := run(filepath.Join(scratch, "novapanelctl"), []string{"--socket", socket, verb}, runner.RunOpts{Dir: scratch, Env: os.Environ()})
		if exit != 0 {
			t.Fatalf("root %s: exit=%d %s", verb, exit, text)
		}
		var reply struct {
			ID   string
			OK   bool
			Data json.RawMessage
		}
		if err := json.Unmarshal([]byte(text), &reply); err != nil || !reply.OK || !id.Valid(reply.ID) || len(reply.Data) == 0 {
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
 s.sendall((json.dumps({"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":verb,"args":{},"actor":"owner"})+"\n").encode())
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
	text, exit := run(filepath.Join(scratch, "novapanelctl"), []string{"--socket", socket, "service.status", "nginx"}, runner.RunOpts{Dir: scratch})
	var status struct {
		OK   bool
		Data struct {
			Unit            string
			Active, Enabled bool
			State           string
		}
	}
	if err := json.Unmarshal([]byte(text), &status); err != nil || exit != 0 || !status.OK || status.Data.Unit != "nginx.service" || !status.Data.Active || !status.Data.Enabled || status.Data.State != "active" {
		t.Fatalf("service.status: exit=%d %s", exit, text)
	}
	for _, unit := range []string{"unlisted.service", "../nginx.service"} {
		text, exit := run(filepath.Join(scratch, "novapanelctl"), []string{"--socket", socket, "service.status", unit}, runner.RunOpts{Dir: scratch})
		if exit == 0 || !strings.Contains(text, `"code":"invalid_argument"`) {
			t.Fatalf("invalid unit %q: exit=%d %s", unit, exit, text)
		}
	}
	text, exit = run(filepath.Join(scratch, "novapanelctl"), []string{"--socket", socket, "service.restart", "nginx"}, runner.RunOpts{Dir: scratch})
	var enqueued struct {
		OK   bool
		Data struct{ Job struct{ ID string } }
	}
	if err := json.Unmarshal([]byte(text), &enqueued); err != nil || exit != 0 || !enqueued.OK || !id.Valid(enqueued.Data.Job.ID) {
		t.Fatalf("service.restart enqueue: exit=%d %s", exit, text)
	}
	client, err := rpc.Dial(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		reply, err := client.Call(context.Background(), "job.status", map[string]string{"id": enqueued.Data.Job.ID}, "")
		raw, _ := json.Marshal(reply.Data)
		var job struct{ State string }
		if err != nil || json.Unmarshal(raw, &job) != nil {
			t.Fatalf("job.status: %+v %v", reply, err)
		}
		if job.State == "done" {
			break
		}
		if job.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("restart did not complete: %s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	client.Close()
	t.Log("safe injected daemon: root ping/info/status and restart job accepted; invalid units refused; root web refused; web ping accepted and owner info denied; disallowed uid rejected by SO_PEERCRED")
	stop := func() {
		t.Helper()
		// Identify only the process serving our scratch socket, not a host service.
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := conn.SyscallConn()
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		var cred *syscall.Ucred
		var credErr error
		err = raw.Control(func(fd uintptr) {
			cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		})
		conn.Close()
		if err != nil || credErr != nil {
			t.Fatalf("agent peer credentials: %v %v", err, credErr)
		}
		if err := syscall.Kill(int(cred.Pid), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case r := <-done:
			running = false
			_, statErr := os.Lstat(socket)
			if r.err != nil || r.exit != 0 || !os.IsNotExist(statErr) {
				t.Fatalf("SIGTERM must exit cleanly and remove socket: exit=%d err=%v socket_err=%v output=%s", r.exit, r.err, statErr, r.text)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("SIGTERM shutdown timed out")
		}
	}
	stop()
	start()
	running = true
	waitSocket()
	if text, exit := run(filepath.Join(scratch, "novapanelctl"), []string{"--socket", socket, "ping"}, runner.RunOpts{Dir: scratch, Env: os.Environ()}); exit != 0 {
		t.Fatalf("restarted agent ping: exit=%d %s", exit, text)
	}
	stop()
	t.Log("SIGTERM: clean exit and socket removal; restart on the same path accepted ping and stopped cleanly")
}

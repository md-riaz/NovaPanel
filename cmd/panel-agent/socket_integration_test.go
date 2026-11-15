package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/agent"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
	"novapanel/internal/validate"
)

func TestSafeDaemonHelper(t *testing.T) {
	if os.Getenv("NOVAPANEL_SAFE_HELPER") != "1" {
		return
	}
	var cfg config.Config
	if e := json.Unmarshal([]byte(os.Getenv("NOVAPANEL_SAFE_CONFIG")), &cfg); e != nil {
		t.Fatal(e)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, unix.SIGTERM)
	defer stop()
	a := agent.NewWithDependencies(cfg, safeRunner{}, &validate.V{})
	if os.Getenv("NOVAPANEL_SAFE_CONSOLE") == "1" {
		configureConsoleFixture(t, a)
	}
	if e := serve(ctx, a); e != nil {
		t.Fatal(e)
	}
}

func TestSafeSocketPeerAndSignal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root for scratch socket and peer credentials")
	}
	a := scratchAgent(t)
	cfg, _ := json.Marshal(a.Cfg)
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		done := make(chan error, 1)
		go func() {
			out, stderr, code, e := (exec.Real{}).Run(ctx, executable, []string{"-test.run=^TestSafeDaemonHelper$"}, exec.RunOpts{Env: append(os.Environ(), "NOVAPANEL_SAFE_HELPER=1", "NOVAPANEL_SAFE_CONFIG="+string(cfg))})
			if e != nil || code != 0 {
				done <- fmt.Errorf("helper exit=%d err=%v: %s%s", code, e, out, stderr)
			} else {
				done <- nil
			}
		}()
		waitAgent(t, a, done)
		client, e := rpc.Dial(a.Cfg.SocketPath)
		if e != nil {
			t.Fatal(e)
		}
		if r, e := client.Call(context.Background(), "service.status", map[string]string{"unit": "nginx.service"}, ""); e != nil || !r.OK {
			t.Fatalf("status: %+v %v", r, e)
		}
		client.Close()
		// Both UIDs can connect by group permissions; only SO_PEERCRED distinguishes them.
		probe := `import json,os,socket,sys
os.setgroups([]);os.setgid(int(sys.argv[2]));os.setuid(int(sys.argv[1]))
s=socket.socket(socket.AF_UNIX);s.settimeout(3);s.connect("agent.sock")
print("CONNECTED",flush=True)
try:
 s.sendall((json.dumps({"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","verb":"agent.info","args":{},"actor":"owner"})+"\n").encode());data=s.recv(4096);print(data.decode() if data else "PEER_CLOSED")
except (ConnectionResetError,BrokenPipeError):print("PEER_CLOSED")
`
		probePath := filepath.Join(filepath.Dir(a.Cfg.SocketPath), "peer_probe.py")
		if e := os.WriteFile(probePath, []byte(probe), 0600); e != nil {
			t.Fatal(e)
		}
		for _, tc := range []struct{ uid, want string }{{"65534", "unauthenticated"}, {"65533", "PEER_CLOSED"}} {
			out, stderr, code, e := (exec.Real{}).Run(context.Background(), "/usr/bin/python3", []string{probePath, tc.uid, "0"}, exec.RunOpts{Dir: filepath.Dir(a.Cfg.SocketPath)})
			if e != nil || code != 0 || !strings.Contains(string(out), "CONNECTED") || !strings.Contains(string(out), tc.want) {
				t.Fatalf("peer %s: %s %s %d %v", tc.uid, out, stderr, code, e)
			}
		}
		conn, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: a.Cfg.SocketPath, Net: "unix"})
		if e != nil {
			t.Fatal(e)
		}
		raw, e := conn.SyscallConn()
		if e != nil {
			t.Fatal(e)
		}
		var cred *unix.Ucred
		var ce error
		if e = raw.Control(func(fd uintptr) { cred, ce = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); e != nil || ce != nil {
			t.Fatalf("credentials: %v %v", e, ce)
		}
		conn.Close()
		if e = unix.Kill(int(cred.Pid), unix.SIGTERM); e != nil {
			t.Fatal(e)
		}
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("signal shutdown timeout")
		}
		cancel()
		for _, p := range []string{a.Cfg.SocketPath, a.Cfg.DBPath + ".pid"} {
			if _, e := os.Stat(p); !os.IsNotExist(e) {
				t.Fatalf("not cleaned: %s %v", p, e)
			}
		}
	}
}

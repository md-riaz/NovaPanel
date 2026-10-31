package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
)

func TestSafeCrashRestart(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	a := scratchAgent(t)
	cfg, _ := json.Marshal(a.Cfg)
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		out, stderr, code, e := (exec.Real{}).Run(ctx, executable, []string{"-test.run=^TestSafeDaemonHelper$"}, exec.RunOpts{Env: append(os.Environ(), "NOVAPANEL_SAFE_HELPER=1", "NOVAPANEL_SAFE_CONFIG="+string(cfg))})
		done <- fmt.Errorf("exit %d: %v %s%s", code, e, out, stderr)
	}()
	waitAgent(t, a, done)
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
		t.Fatal(e, ce)
	}
	conn.Close()
	if e = unix.Kill(int(cred.Pid), unix.SIGKILL); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("crashed helper did not exit")
	}
	if _, e = os.Lstat(a.Cfg.SocketPath); e != nil {
		t.Fatal("crash did not leave socket", e)
	}
	restartCtx, stop := context.WithCancel(context.Background())
	defer stop()
	restarted := make(chan error, 1)
	go func() { restarted <- serveScratch(restartCtx, a) }()
	waitAgent(t, a, restarted)
	client, e := rpc.Dial(a.Cfg.SocketPath)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Call(context.Background(), "ping", map[string]any{}, ""); e != nil {
		t.Fatal(e)
	}
	client.Close()
	stop()
	select {
	case e := <-restarted:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restart shutdown timed out")
	}
}

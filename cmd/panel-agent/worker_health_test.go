package main

import (
	"context"
	"os"
	"testing"
	"time"

	"novapanel/internal/audit"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
)

type blockedAuditRunner struct{ started, release chan struct{} }

func (r blockedAuditRunner) Run(ctx context.Context, p string, args []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/bin/systemctl" && args[0] == "restart" {
		close(r.started)
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, nil, -1, ctx.Err()
		}
	}
	return (safeRunner{}).Run(ctx, p, args, o)
}
func TestWorkerAuditFailureDoesNotStopReadRPC(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	a := scratchAgent(t)
	r := blockedAuditRunner{make(chan struct{}), make(chan struct{})}
	a.Exec = r
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveScratch(ctx, a) }()
	waitAgent(t, a, done)
	c, e := rpc.Dial(a.Cfg.SocketPath)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.Call(ctx, "service.restart", map[string]string{"unit": "nginx.service"}, ""); e != nil {
		t.Fatal(e)
	}
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("runner not started")
	}
	if e = a.Audit.Record(audit.Event{Outcome: "invalid"}); e == nil {
		t.Fatal("expected sticky failure")
	}
	close(r.release)
	select {
	case e := <-done:
		t.Fatalf("worker audit failure stopped daemon: %v", e)
	case <-time.After(150 * time.Millisecond):
	}
	if _, e = c.Call(ctx, "ping", map[string]any{}, ""); e != nil {
		t.Fatal(e)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timeout")
	}
}

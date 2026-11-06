package main

import (
	"context"
	"errors"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
	"os"
	"testing"
)

type failedMariaDBRunner struct{ safeRunner }

func (r failedMariaDBRunner) Run(ctx context.Context, path string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	if path == "/usr/bin/mariadb" {
		return nil, nil, 1, errors.New("unavailable")
	}
	return r.safeRunner.Run(ctx, path, args, opts)
}
func TestMariaDBUnavailableDoesNotPreventReadiness(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root for socket ownership")
	}
	a := scratchAgent(t)
	a.Exec = failedMariaDBRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, a) }()
	waitAgent(t, a, done)
	c, err := rpc.Dial(a.Cfg.SocketPath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	r, err := c.Call(context.Background(), "service.status", map[string]string{"unit": "nginx.service"}, "")
	c.Close()
	if err != nil || !r.OK {
		cancel()
		t.Fatalf("service status: %+v %v", r, err)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

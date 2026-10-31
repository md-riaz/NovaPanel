package update

import (
	"context"
	"net"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"path/filepath"
	"testing"
	"time"
)

func TestPingRealSocketAndDeadline(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := rpc.NewServer(agent.New())
	s.Registry = map[string]rpc.Verb{"ping": {Name: "ping", Public: true, ReadOnly: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) {
		return map[string]string{"version": "test"}, nil
	}}}
	done := make(chan struct{})
	go func() { defer close(done); _ = s.ServeContext(ctx, l) }()
	if err = Ping(context.Background(), socket, 1000); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	l, err = net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan struct{})
	go func() {
		c, e := l.Accept()
		if e == nil {
			defer c.Close()
			<-accepted
		}
	}()
	start := time.Now()
	err = Ping(context.Background(), socket, 25)
	close(accepted)
	if err != context.DeadlineExceeded || time.Since(start) > time.Second {
		t.Fatalf("deadline: %v", err)
	}
}

func TestPingUsesConfiguredDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Ping(ctx, "/not-a-real-socket", 25); err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	if err := Ping(context.Background(), "/not-a-real-socket", 0); err == nil {
		t.Fatal("accepted unset deadline")
	}
}

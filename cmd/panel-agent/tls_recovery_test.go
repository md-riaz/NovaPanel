package main

import (
	"context"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUnresolvedTLSStartupKeepsReadRPC(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	a := scratchAgent(t)
	if err := os.MkdirAll(filepath.Dir(a.Cfg.DBPath), 0700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(a.Cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.BeginTLSIntent(context.Background(), []byte(`{"version":999}`)); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, a) }()
	waitAgent(t, a, done)
	c, err := rpc.Dial(a.Cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if reply, err := c.Call(ctx, "service.status", map[string]string{"unit": "nginx.service"}, ""); err != nil || !reply.OK {
		t.Fatalf("read RPC: %+v %v", reply, err)
	}
	if reply, err := c.Call(ctx, "service.restart", map[string]string{"unit": "nginx.service"}, ""); err == nil || reply.OK || reply.Code != "precondition_failed" {
		t.Fatalf("mutation was not blocked: %+v %v", reply, err)
	}
	if reply, err := c.Call(ctx, "service.status", map[string]string{"unit": "nginx.service"}, ""); err != nil || !reply.OK {
		t.Fatalf("read RPC after refusal: %+v %v", reply, err)
	}
	select {
	case err = <-done:
		t.Fatalf("recovery stopped daemon: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown timeout")
	}
}

package main

import (
	"context"
	"novapanel/internal/rpc"
	"os"
	"testing"
	"time"
)

func TestServeKeepsFatalWorkerErrors(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	a := scratchAgent(t)
	ready := make(chan struct{}, 1)
	a.Exec = safeRunner{ready: ready}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serveScratch(ctx, a) }()
	waitAgent(t, a, done)
	client, e := rpc.Dial(a.Cfg.SocketPath)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Call(ctx, "service.status", map[string]string{"unit": "nginx.service"}, ""); e != nil {
		t.Fatal(e)
	}
	client.Close()
	<-ready
	if e := a.Store.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("lost fatal store error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fatal worker error did not stop daemon")
	}
}

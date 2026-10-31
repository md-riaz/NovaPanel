package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func TestShutdownKeepsOwnershipUntilStorageDrained(t *testing.T) {
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
	if _, e = client.Call(context.Background(), "service.status", map[string]string{"unit": "nginx.service"}, ""); e != nil {
		t.Fatal(e)
	}
	client.Close()
	<-ready
	entered := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- a.Store.Tx(context.Background(), func(tx *sql.Tx) error { close(entered); <-release; return nil })
	}()
	<-entered
	cancel()
	select {
	case <-done:
		t.Fatal("shutdown returned before transaction drained")
	case <-time.After(30 * time.Millisecond):
	}
	if _, e := os.Lstat(a.Cfg.SocketPath); e != nil {
		t.Fatalf("socket ownership released early: %v", e)
	}
	if _, e := os.Stat(a.Cfg.DBPath + ".pid"); e != nil {
		t.Fatalf("pidfile released early: %v", e)
	}
	if other, e := store.Open(a.Cfg.DBPath); e == nil {
		other.Close()
		t.Fatal("lock released early")
	}
	close(release)
	<-txDone
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("storage shutdown timeout")
	}
	reopened, e := store.Open(a.Cfg.DBPath)
	if e != nil {
		t.Fatal(e)
	}
	reopened.Close()
}

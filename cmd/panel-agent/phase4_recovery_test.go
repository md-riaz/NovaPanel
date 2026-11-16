package main

import (
	"context"
	"encoding/json"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestP4StartupGate(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	a := scratchAgent(t)
	a.Cfg.SitesRoot = filepath.Join(t.TempDir(), "sites")
	a.Cfg.TLSDir = filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(filepath.Dir(a.Cfg.DBPath), 0700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(a.Cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	j := store.Job{ID: "01K8WMXZF57CGNZKX1FZ38DZMT", Verb: "database.delete", CreatedBy: "departed-token", Role: "token", Scopes: []string{"databases:write"}, State: "queued", Args: json.RawMessage(`{}`), ObjectKey: "db:01K8WMXZF57CGNZKX1FZ38DZMQ"}
	pair := store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR"}
	if err = st.CreateDatabasePrimary(context.Background(), pair); err != nil {
		t.Fatal(err)
	}
	if _, err = st.PrepareOperation(context.Background(), j, store.JobClaim{}, store.OperationRequest{Kind: store.DatabaseDelete, DatabaseID: pair.ID}, nil, nil); err != nil {
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
	for _, verb := range []string{"service.restart", "session.start"} {
		args := map[string]string{}
		if verb == "service.restart" {
			args["unit"] = "nginx.service"
		}
		r, _ := c.Call(ctx, verb, args, "")
		if r.OK || r.Code != "precondition_failed" {
			t.Errorf("mutation %s: %+v", verb, r)
		}
	}
	r, err := c.Call(ctx, "service.status", map[string]string{"unit": "nginx.service"}, "")
	if err != nil || !r.OK {
		t.Fatalf("read: %+v %v", r, err)
	}
	got, err := a.Store.GetJob(ctx, j.ID)
	if err != nil || got.CreatedBy != j.CreatedBy || got.Role != j.Role {
		t.Fatalf("authority changed: %+v %v", got, err)
	}
	if _, err = a.Jobs.Enqueue(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root", Role: "owner"}), "reconcile", map[string]any{}, "all"); err == nil {
		t.Error("direct enqueue bypassed startup gate")
	}
	if err = a.Scheduler.Tick(ctx); err == nil {
		t.Error("scheduler bypassed startup gate")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown deadlock")
	}
}

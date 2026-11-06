package rpc

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

type sealedArgs struct {
	ID       string `json:"id"`
	Password string `json:"password"`
}

func TestSealedJobDispatch(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	a.Jobs = jobs.NewQueue(st, sealer, log)
	s := NewServer(a)
	called := false
	s.Registry = map[string]Verb{"db.test": {Name: "db.test", Slow: true, SealedInput: true, MinRole: Admin, Scope: "databases:write", NewArgs: func() any { return &sealedArgs{} }, ObjectKey: func(v any) string { return "db:" + v.(*sealedArgs).ID }, Run: func(context.Context, *agent.Agent, any) (any, error) { return nil, nil }, Exec: func(context.Context, *agent.Agent, *jobs.Job) error { called = true; return nil }}}
	ctx := jobs.WithMetadata(context.Background(), jobs.Metadata{Role: "admin", CreatedBy: "owner"})
	r, err := a.Jobs.EnqueueSealed(ctx, "db.test", sealedArgs{ID: "one", Password: "secret"}, "db:one")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ExecuteJob(ctx, &r.Job); err != nil || !called {
		t.Fatalf("execute: %v, called=%v", err, called)
	}
	j := r.Job
	j.ObjectKey = "db:other"
	called = false
	if err = s.ExecuteJob(ctx, &j); err == nil || called {
		t.Fatal("accepted altered binding")
	}
}

package rpc_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestSlowDispatchCapturesBoundaryIdentity(t *testing.T) {
	st, e := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	log, e := audit.Open(st, filepath.Join(t.TempDir(), "audit.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	seal, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Jobs = jobs.NewQueue(st, seal, log)
	s := rpc.NewServer(a)
	s.Registry = map[string]rpc.Verb{"slow": {Name: "slow", MinRole: rpc.Admin, Scope: "system:write", Slow: true, Effectful: true, NewArgs: func() any { return &struct{}{} }, ObjectKey: func(any) string { return "system" }, Run: func(ctx context.Context, a *agent.Agent, args any) (any, error) {
		return a.Jobs.Enqueue(ctx, "slow", args, "system")
	}, Exec: func(context.Context, *agent.Agent, *jobs.Job) error { return nil }}}
	for _, c := range []rpc.Caller{{Kind: "token", Session: "token-id", UserID: "wrong-user", Scopes: []string{"system:write"}, Peer: 42}, rpc.RootCaller(0)} {
		r := s.Dispatch(rpc.WithCaller(context.Background(), c), rpc.Request{Verb: "slow", Actor: "forged", Args: json.RawMessage(`{}`)})
		if !r.OK {
			t.Fatalf("slow refused: %+v", r)
		}
		result := r.Data.(jobs.EnqueueResult)
		j, e := st.GetJob(context.Background(), result.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		want := c.Session
		if want == "" {
			want = "local-root"
		}
		if j.CreatedBy != want || !j.Effectful || j.Actor != "forged" {
			t.Fatalf("authority: %+v", j)
		}
	}
}

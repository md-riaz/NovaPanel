package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestQueuedDurablePrincipal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		caller Caller
		bad    bool
		want   map[string]any
	}{
		{"user", Caller{Kind: "user", Role: "admin", UserID: "user-id", Session: "session-id", Peer: 42}, false, map[string]any{"kind": "user", "session_id": "session-id"}},
		{"token", Caller{Kind: "token", Session: "token-id", Scopes: []string{"system:write"}, Peer: 42}, false, map[string]any{"kind": "token", "token_id": "token-id"}},
		{"root", RootCaller(0), false, map[string]any{"kind": "local-root"}},
		{"error", Caller{Kind: "user", Role: "admin", Session: "session-id", Peer: 42}, true, map[string]any{"kind": "user", "session_id": "session-id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			st, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			path := filepath.Join(dir, "audit.log")
			l, err := audit.Open(st, path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			a := agent.New()
			a.Store = st
			a.Audit = l
			a.Jobs = jobs.NewQueue(st, nil, l)
			s := NewServer(a)
			s.Registry = map[string]Verb{"slow": {Name: "slow", Slow: true, MinRole: Admin, Scope: "system:write", NewArgs: func() any { return &struct{}{} }, ObjectKey: func(any) string { return "site:parent" }, Run: func(ctx context.Context, a *agent.Agent, args any) (any, error) {
				return a.Jobs.Enqueue(ctx, "slow", args, "site:parent")
			}, Exec: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
				m, ok := jobs.MetadataFrom(ctx)
				if !ok || m.CreatedBy != j.CreatedBy {
					return Errf("forbidden", "missing durable authority")
				}
				if tc.bad {
					return Errf("invalid_argument", "nps_private_error")
				}
				return nil
			}}}
			reply := s.Dispatch(WithCaller(ctx, tc.caller), Request{Verb: "slow", Actor: "forged-root", Args: json.RawMessage(`{}`)})
			if !reply.OK {
				t.Fatalf("dispatch %+v", reply)
			}
			jid := reply.Data.(jobs.EnqueueResult).Job.ID
			// Reopen both durable sinks before worker execution. No live caller survives.
			if err = l.Close(); err != nil {
				t.Fatal(err)
			}
			if err = st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			l, err = audit.Open(st, path)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			a.Store = st
			a.Audit = l
			a.Jobs = jobs.NewQueue(st, nil, l)
			a.Jobs.SetRunner(s.ExecuteJob)
			worker, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(worker) }()
			defer func() {
				cancel()
				if e := <-done; e != nil {
					t.Error(e)
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			finished := false
			for time.Now().Before(deadline) {
				j, e := st.GetJob(ctx, jid)
				if e != nil {
					t.Fatal(e)
				}
				if j.State == "done" || j.State == "failed" {
					want := "done"
					if tc.bad {
						want = "failed"
					}
					if j.State != want {
						t.Fatalf("job %+v", j)
					}
					finished = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !finished {
				t.Fatal("worker timeout")
			}
			events, err := st.ListEvents(ctx, 20, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 4 {
				t.Fatalf("events %d want 4", len(events))
			}
			stages := map[string]int{}
			for _, ev := range events {
				var detail map[string]any
				if err = json.Unmarshal(ev.Detail, &detail); err != nil {
					t.Fatal(err)
				}
				if detail["job_id"] == jid {
					stages[detail["stage"].(string)]++
					if !reflect.DeepEqual(detail["principal"], tc.want) {
						t.Fatalf("queued principal %#v want %#v", detail["principal"], tc.want)
					}
					if ev.Actor != "forged-root" || ev.ObjectKey != "site:parent" {
						t.Fatalf("attribution %+v", ev)
					}
				}
			}
			if stages["enqueue"] != 1 || stages["exec"] != 1 {
				t.Fatal(stages)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(b, []byte("nps_private_error")) {
				t.Fatal("error secret leaked")
			}
			for _, line := range splitAuditLines(b) {
				var f struct {
					ID     string          `json:"id"`
					Detail json.RawMessage `json:"detail"`
				}
				if err = json.Unmarshal(line, &f); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, ev := range events {
					if ev.ID == f.ID {
						found = true
						if !bytes.Equal(ev.Detail, f.Detail) {
							t.Fatal("mirror detail mismatch")
						}
					}
				}
				if !found {
					t.Fatal("file event absent from mirror")
				}
			}
		})
	}
}

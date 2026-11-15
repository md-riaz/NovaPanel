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
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyTokenWorkerAuditPrincipal(t *testing.T) {
	for _, restart := range []bool{false, true} {
		for _, outcome := range []string{"success", "execution-error", "mfa-refusal"} {
			name := outcome
			if restart {
				name += "/restart"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				dir := t.TempDir()
				dbPath, logPath := filepath.Join(dir, "db"), filepath.Join(dir, "audit.log")
				st, err := store.Open(dbPath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { st.Close() }()
				log, err := audit.Open(st, logPath)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { log.Close() }()
				a := agent.New()
				a.Store, a.Audit = st, log
				a.Jobs = jobs.NewQueue(st, nil, log)
				s := NewServer(a)
				var calls atomic.Int32
				s.Registry = map[string]Verb{"legacy": {
					Name: "legacy", Slow: true, MinRole: Admin, Scope: "system:write", MFA: true,
					NewArgs: func() any { return &struct{}{} }, ObjectKey: func(any) string { return "system" },
					Exec: func(ctx context.Context, _ *agent.Agent, j *jobs.Job) error {
						calls.Add(1)
						m, ok := jobs.MetadataFrom(ctx)
						if !ok || m.CreatedBy != j.CreatedBy || m.Role != "" || !reflect.DeepEqual(m.Scopes, []string{"system:write"}) {
							return Errf("forbidden", "missing durable legacy authority")
						}
						if outcome == "execution-error" {
							return Errf("invalid_argument", "nps_private_error")
						}
						return nil
					},
				}}
				// The canonical durable schema permits empty-role, scope-bearing token snapshots.
				m := jobs.Metadata{CreatedBy: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Actor: "forged-owner", Scopes: []string{"system:write"}, Automation: outcome != "mfa-refusal"}
				r, err := a.Jobs.Enqueue(jobs.WithMetadata(ctx, m), "legacy", struct{}{}, "system")
				if err != nil {
					t.Fatal(err)
				}
				if restart {
					if err = log.Close(); err != nil {
						t.Fatal(err)
					}
					if err = st.Close(); err != nil {
						t.Fatal(err)
					}
					st, err = store.Open(dbPath)
					if err != nil {
						t.Fatal(err)
					}
					log, err = audit.Open(st, logPath)
					if err != nil {
						t.Fatal(err)
					}
					a.Store, a.Audit = st, log
					a.Jobs = jobs.NewQueue(st, nil, log)
				}
				a.Jobs.SetRunner(s.ExecuteJob)
				worker, cancel := context.WithCancel(ctx)
				done := make(chan error, 1)
				go func() { done <- a.Jobs.Run(worker) }()
				defer func() {
					cancel()
					if err := <-done; err != nil {
						t.Error(err)
					}
				}()
				deadline := time.Now().Add(5 * time.Second)
				for {
					j, err := st.GetJob(ctx, r.Job.ID)
					if err != nil {
						t.Fatal(err)
					}
					if j.State == "done" || j.State == "failed" {
						wantState, wantCode, wantCalls := "done", "", int32(1)
						if outcome == "execution-error" {
							wantState, wantCode = "failed", "invalid_argument"
						} else if outcome == "mfa-refusal" {
							wantState, wantCode, wantCalls = "failed", "forbidden", 0
						}
						if j.State != wantState || j.Code != wantCode || calls.Load() != wantCalls {
							t.Fatalf("job state=%s code=%s calls=%d", j.State, j.Code, calls.Load())
						}
						if j.CreatedBy != m.CreatedBy || j.Role != "" || !reflect.DeepEqual(j.Scopes, m.Scopes) || j.Automation != m.Automation {
							t.Fatalf("durable authority changed: %+v", j)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("worker timeout")
					}
					time.Sleep(time.Millisecond)
				}
				events, err := st.ListEvents(ctx, 10, "")
				if err != nil {
					t.Fatal(err)
				}
				if len(events) != 2 {
					t.Fatalf("events=%d want 2", len(events))
				}
				wantPrincipal := map[string]any{"kind": "token", "token_id": m.CreatedBy}
				stages := map[string]int{}
				byID := map[string]json.RawMessage{}
				for _, ev := range events {
					var d map[string]any
					if err = json.Unmarshal(ev.Detail, &d); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(d["principal"], wantPrincipal) || d["job_id"] != r.Job.ID || ev.Actor != m.Actor {
						t.Errorf("legacy token attribution: actor=%s detail=%s", ev.Actor, ev.Detail)
					}
					stage := d["stage"].(string)
					wantOutcome, wantCode := "ok", ""
					if stage == "exec" && outcome != "success" {
						wantOutcome, wantCode = "error", "invalid_argument"
						if outcome == "mfa-refusal" {
							wantCode = "forbidden"
						}
					}
					if ev.Outcome != wantOutcome || ev.Code != wantCode {
						t.Errorf("%s outcome=%s code=%s", stage, ev.Outcome, ev.Code)
					}
					stages[stage]++
					byID[ev.ID] = ev.Detail
				}
				if !reflect.DeepEqual(stages, map[string]int{"enqueue": 1, "exec": 1}) {
					t.Fatal(stages)
				}
				b, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(b, []byte("nps_private_error")) {
					t.Fatal("execution error leaked")
				}
				lines := splitAuditLines(b)
				if len(lines) != len(events) {
					t.Fatalf("file events=%d mirror events=%d", len(lines), len(events))
				}
				for _, line := range lines {
					var ev struct {
						ID     string          `json:"id"`
						Detail json.RawMessage `json:"detail"`
					}
					if err = json.Unmarshal(line, &ev); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(byID[ev.ID], ev.Detail) {
						t.Fatal("file/mirror mismatch")
					}
					delete(byID, ev.ID)
				}
			})
		}
	}
}

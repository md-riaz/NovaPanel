//go:build wordpress_offline_lab && linux

package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/store"
)

func TestCompositeVerifiedRecovery(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "committed"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()
			st, err := store.Open(filepath.Join(root, "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			log, err := audit.Open(st, filepath.Join(root, "audit.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			sid, _ := id.New()
			did, _ := id.New()
			if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
				t.Fatal(err)
			}
			if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
				t.Fatal(err)
			}
			ngroot := filepath.Join(root, "nginx")
			for _, dir := range []string{"sites-available", "sites-enabled"} {
				if err = os.MkdirAll(filepath.Join(ngroot, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			effects := 0
			a := agent.New()
			a.Store = st
			a.Nginx = &nginx.Applier{Root: ngroot, Runner: poolReplayRunner{}, Probe: func(context.Context, nginx.Desired) error { effects++; return nil }}
			a.Jobs = jobs.NewQueue(st, nil, log)
			a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { return siteUpdateExec(ctx, a, j) })
			if err = a.Jobs.PrepareOperations(ctx); err != nil {
				t.Fatal(err)
			}
			if err = a.Jobs.PrepareStartup(ctx); err != nil {
				t.Fatal(err)
			}
			maintenance := true
			request := &store.CompositeSiteUpdate{SiteID: sid, PrimaryDomainID: &did, Maintenance: &maintenance}
			if failed {
				force := true
				request.PrimaryDomainID, request.ForceHTTPS = nil, &force
			}
			owner := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "owner"})
			result, err := a.Jobs.EnqueueOperation(owner, store.OperationRequest{Kind: store.SiteUpdate, SiteID: sid, SiteUpdate: request}, nil)
			if err != nil {
				t.Fatal(err)
			}
			run, cancel := context.WithCancel(ctx)
			store.SetFreshCheckpointTestHook(func(_ context.Context, jid, checkpoint string) {
				if jid == result.Job.ID && checkpoint == "verified" {
					cancel()
				}
			})
			if err = a.Jobs.Run(run); err != nil {
				t.Fatal(err)
			}
			store.SetFreshCheckpointTestHook(nil)
			t.Cleanup(func() { store.SetFreshCheckpointTestHook(nil) })
			op, err := st.LoadOperation(ctx, result.Job.ID)
			if err != nil || op.Checkpoint != "verified" {
				t.Fatalf("checkpoint: %+v %v", op, err)
			}
			before, err := st.OperationChildren(ctx, result.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeEffects := effects
			if err = st.Tx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE phase4_operations SET expires=1 WHERE job_id=?`, result.Job.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err = st.ExpireOperations(ctx); err != nil {
				t.Fatal(err)
			}
			if err = st.RecoverJobs(ctx, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			run, stop := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(run) }()
			defer func() { stop(); <-done }()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				j, e := st.GetJob(ctx, result.Job.ID)
				if e == nil && (j.State == "done" || j.State == "failed") {
					want, code := "done", ""
					if failed {
						want, code = "failed", "precondition_failed"
					}
					if j.State != want || j.Code != code || effects != beforeEffects {
						t.Fatalf("replay: %+v effects=%d before=%d", j, effects, beforeEffects)
					}
					after, e := st.OperationChildren(ctx, j.ID)
					if e != nil || len(after) != len(before) {
						t.Fatalf("children: %+v %v", after, e)
					}
					for i := range before {
						if before[i].JournalRef != after[i].JournalRef {
							t.Fatal("child reran")
						}
					}
					events, e := st.JobEvents(ctx, j.ID)
					if e != nil {
						t.Fatal(e)
					}
					found := false
					for _, event := range events {
						if event.Msg == want {
							found = true
						}
						if !failed && event.Msg == "failed" {
							t.Fatal("failed event on successful replay")
						}
					}
					if !found {
						t.Fatalf("missing %s event: %+v", want, events)
					}
					raw, e := os.ReadFile(filepath.Join(root, "audit.log"))
					if e != nil {
						t.Fatal(e)
					}
					auditFound := false
					for _, line := range bytes.Split(raw, []byte("\n")) {
						var record struct {
							Outcome, Code string
							Detail        struct {
								JobID              string `json:"job_id"`
								Stage, Publication string
							}
						}
						if json.Unmarshal(line, &record) != nil || record.Detail.JobID != j.ID || record.Detail.Stage != "exec" || record.Detail.Publication == "intent" {
							continue
						}
						wantOutcome := "ok"
						if failed {
							wantOutcome = "error"
						}
						if record.Outcome != wantOutcome || record.Code != code {
							t.Fatalf("wrong audit: %s", line)
						}
						auditFound = true
					}
					if !auditFound {
						t.Fatalf("missing terminal audit: %s", raw)
					}
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("replay did not finish")
		})
	}
}

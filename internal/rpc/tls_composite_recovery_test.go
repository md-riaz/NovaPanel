package rpc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/store"
)

func TestTLSRecoveryDefersComposite(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid, jid := "01K8WMXZF57CGNZKX1FZ38DZMA", "01K8WMXZF57CGNZKX1FZ38DZMC"
	if _, err = s.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	force, maintenance := true, true
	v := &store.CompositeSiteUpdate{SiteID: sid, ForceHTTPS: &force, Maintenance: &maintenance}
	args, _ := json.Marshal(v)
	j := store.Job{ID: jid, Verb: "site.update", ObjectKey: "site:" + sid, CreatedBy: "local-root", Role: "owner", Args: args, State: "queued"}
	if _, err = s.PrepareOperation(ctx, j, store.JobClaim{}, store.OperationRequest{Kind: store.SiteUpdate, SiteID: sid, SiteUpdate: v}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, jid, 1); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"job_id": jid})
	if err = s.BeginCompositeTLSIntent(ctx, jid, raw); err != nil {
		t.Fatal(err)
	}
	server := &Server{Agent: &agent.Agent{Store: s}}
	if got, err := server.prepareTLSRecovery(ctx); err != nil || got != nil {
		t.Fatalf("composite was recovered twice: %+v %v", got, err)
	}
	if pending, err := s.TLSIntent(ctx); err != nil || len(pending) == 0 {
		t.Fatalf("intent lost: %s %v", pending, err)
	}
	if err = s.ClearTLSIntent(ctx, raw); err != nil {
		t.Fatal(err)
	}
	otherID := "01K8WMXZF57CGNZKX1FZ38DZMD"
	other := store.Job{ID: otherID, Verb: "ssl.upload", ObjectKey: "site:" + sid, CreatedBy: "local-root", Role: "owner", Args: []byte(`{}`), State: "running"}
	if err = s.CreateJob(ctx, other); err != nil {
		t.Fatal(err)
	}
	orphan := []byte(`{"job_id":"` + otherID + `"}`)
	if err = s.BeginTLSIntent(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	if got, err := server.prepareTLSRecovery(ctx); err == nil || got != nil {
		t.Fatalf("orphan intent dispatched: %+v %v", got, err)
	}
	job, err := s.GetJob(ctx, jid)
	if err != nil || job.Code != "" {
		t.Fatalf("orphan altered composite: %+v %v", job, err)
	}
	otherAfter, err := s.GetJob(ctx, otherID)
	if err != nil || otherAfter.State != other.State || otherAfter.Code != other.Code || otherAfter.Attempts != other.Attempts {
		t.Fatalf("orphan altered unrelated job: %+v %v", otherAfter, err)
	}
	anomaly, err := s.Setting(ctx, "tls:publication:unreconciled:v1")
	if err != nil || string(anomaly) != "true" {
		t.Fatalf("orphan anomaly missing: %s %v", anomaly, err)
	}
	if err = s.ClearTLSIntent(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte(nil), raw...), []byte(` {}`)...)
	if err = s.BeginTLSIntent(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if got, err := server.prepareTLSRecovery(ctx); err != nil || got != nil {
		t.Fatalf("quarantined composite must reach its finalizer: %+v %v", got, err)
	}
	job, err = s.GetJob(ctx, jid)
	if err != nil || job.State != "running" || job.Code != "agent_error" {
		t.Fatalf("disposition: %+v %v", job, err)
	}
	if err = s.RecoverJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	job, err = s.GetJob(ctx, jid)
	if err != nil || job.State != "queued" || job.Code != "agent_error" {
		t.Fatalf("recovery lost quarantine: %+v %v", job, err)
	}
	pending, err := s.TLSIntent(ctx)
	if err != nil || string(pending) != string(changed) {
		t.Fatalf("intent lost: %s %v", pending, err)
	}
}

func TestTLSRecoveryRecordsOrphanWithoutOwner(t *testing.T) {
	for _, raw := range []string{`{"job_id":"01K8WMXZF57CGNZKX1FZ38DZMD"}`, `{"job_id":`} {
		t.Run(raw, func(t *testing.T) {
			ctx := context.Background()
			db := filepath.Join(t.TempDir(), "panel.db")
			s, err := store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.BeginTLSIntent(ctx, []byte(raw)); err != nil {
				t.Fatal(err)
			}
			server := &Server{Agent: &agent.Agent{Store: s}}
			if job, err := server.prepareTLSRecovery(ctx); err == nil || job != nil {
				t.Fatalf("orphan dispatched: %+v %v", job, err)
			}
			anomaly, err := s.Setting(ctx, "tls:publication:unreconciled:v1")
			if err != nil || string(anomaly) != "true" {
				t.Fatalf("missing anomaly: %s %v", anomaly, err)
			}
			intent, err := s.TLSIntent(ctx)
			if err != nil || string(intent) != raw {
				t.Fatalf("lost intent: %s %v", intent, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			anomaly, err = s.Setting(ctx, "tls:publication:unreconciled:v1")
			if err != nil || string(anomaly) != "true" {
				t.Fatalf("lost anomaly on restart: %s %v", anomaly, err)
			}
			intent, err = s.TLSIntent(ctx)
			if err != nil || string(intent) != raw {
				t.Fatalf("lost intent on restart: %s %v", intent, err)
			}
		})
	}
}

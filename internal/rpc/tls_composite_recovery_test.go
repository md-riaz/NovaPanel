package rpc

import (
	"context"
	"encoding/json"
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
	j := store.Job{ID: jid, Verb: "site.update", ObjectKey: "site:" + sid, CreatedBy: "local-root", Role: "owner", Args: []byte(`{}`), State: "queued"}
	if _, err = s.PrepareOperation(ctx, j, store.JobClaim{}, store.OperationRequest{Kind: store.SiteUpdate, SiteID: sid, SiteUpdate: v}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, jid, 1); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"job_id": jid})
	if err = s.BeginTLSIntent(ctx, raw); err != nil {
		t.Fatal(err)
	}
	server := &Server{Agent: &agent.Agent{Store: s}}
	if got, err := server.prepareTLSRecovery(ctx); err != nil || got != nil {
		t.Fatalf("composite was recovered twice: %+v %v", got, err)
	}
	if pending, err := s.TLSIntent(ctx); err != nil || len(pending) == 0 {
		t.Fatalf("intent lost: %s %v", pending, err)
	}
}

package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestSiteDeleteRequiresExplicitDatabasePurge(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid := "01K8WMXZF57CGNZKX1FZ38DZMA"
	_, err = s.CreateSite(ctx, Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateDatabasePrimary(ctx, DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", SiteID: sid}); err != nil {
		t.Fatal(err)
	}
	j := Job{ID: "delete-job", Verb: "site.delete", CreatedBy: "owner", State: "queued", Args: []byte(`{}`)}
	if _, err = s.PrepareOperation(ctx, j, JobClaim{}, OperationRequest{Kind: SiteDelete, SiteID: sid}, nil, nil); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("default deletion: %v", err)
	}
	op, err := s.PrepareOperation(ctx, j, JobClaim{}, OperationRequest{Kind: SiteDelete, SiteID: sid, PurgeDatabases: true}, nil, nil)
	if err != nil || !op.Identity.PurgeDatabases || len(op.Identity.Attached) != 1 || len(op.Identity.Attached[0].Accounts) != 1 {
		t.Fatalf("purge journal: %+v %v", op, err)
	}
}

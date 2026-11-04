package store

import (
	"context"
	php "novapanel/internal/render/php"
	"path/filepath"
	"testing"
)

func TestRestoredInterruptedPHPReconcilesBeforeExpiry(t *testing.T) {
	ctx := context.Background()
	clock := &authClock{now: 1000}
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid, did, jid := "01K8WMXZF57CGNZKX1FZ38DZMA", "01K8WMXZF57CGNZKX1FZ38DZMB", "01K8WMXZF57CGNZKX1FZ38DZMC"
	version := "8.3"
	if _, err = s.CreateSite(ctx, Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, Domain{ID: did, FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	op, err := s.PrepareOperation(ctx, Job{ID: jid, Verb: "php.set", ObjectKey: "site:" + sid, CreatedBy: "local-root", Role: "owner", Args: []byte(`{}`), State: "queued"}, JobClaim{}, OperationRequest{Kind: PHPSet, SiteID: sid, PHP: &php.SetRequest{SiteID: sid, Version: version}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, jid, clock.now); err != nil {
		t.Fatal(err)
	}
	clock.now = op.Expires - 1
	if err = s.FailRestoredPHP(ctx, jid, "interrupted"); err != nil {
		t.Fatal(err)
	}
	after, err := s.LoadOperation(ctx, jid)
	if err != nil || after.Checkpoint != "expired" || !after.Reconciled {
		t.Fatalf("restoration lost: %+v %v", after, err)
	}
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	php "novapanel/internal/render/php"
)

func TestPHPExpiryRetainsRecoveryBlockAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	clock := &authClock{now: 1000}
	s.clock = clock
	sid, did, jid := "01K8WMXZF57CGNZKX1FZ38DZMA", "01K8WMXZF57CGNZKX1FZ38DZMB", "01K8WMXZF57CGNZKX1FZ38DZMC"
	version := "8.3"
	if _, err = s.CreateSite(ctx, Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, Domain{ID: did, FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	memory := 512
	req := php.SetRequest{SiteID: sid, Version: version, Limits: php.LimitsPatch{MemoryMB: &memory}}
	j := Job{ID: jid, Verb: "php.set", ObjectKey: "site:" + sid, CreatedBy: "local-root", Role: "owner", Args: []byte(`{}`), State: "queued"}
	op, err := s.PrepareOperation(ctx, j, JobClaim{}, OperationRequest{Kind: PHPSet, SiteID: sid, PHP: &req}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = op.Expires
	if _, err = s.StartJob(ctx, jid, clock.now); err != nil {
		t.Fatal(err)
	}
	if err = s.FailOperation(ctx, jid, "interrupted"); !errors.Is(err, ErrOperationLocked) {
		t.Fatalf("expired PHP failure must retain recovery journal: %v", err)
	}
	still, err := s.LoadOperation(ctx, jid)
	if err != nil || still.Checkpoint != "prepared" {
		t.Fatalf("failure destroyed recovery journal: %+v %v", still, err)
	}
	if _, err = s.ExpireOperations(ctx); !errors.Is(err, ErrOperationLocked) {
		t.Fatalf("expired PHP must block recovery admission: %v", err)
	}
	if err = s.FailRestoredPHP(ctx, jid, "interrupted"); err != nil {
		t.Fatalf("verified restoration must reconcile even at deadline: %v", err)
	}
	still, err = s.LoadOperation(ctx, jid)
	if err != nil || still.Checkpoint != "expired" || !still.Reconciled {
		t.Fatalf("verified restoration not reconciled: %+v %v", still, err)
	}
	if _, err = s.ExpireOperations(ctx); err != nil {
		t.Fatalf("restored PHP must not block: %v", err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE phase4_operations SET checkpoint='expired',reconciled=0 WHERE job_id=?", jid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExpireOperations(ctx); !errors.Is(err, ErrOperationLocked) {
		t.Fatalf("expired unresolved journal must block after reopen: %v", err)
	}
}

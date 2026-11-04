package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	php "novapanel/internal/render/php"
	"novapanel/internal/store"
)

func TestExpiredPHPPeriodicRecoveryRefusalClosesQueue(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	clock := &reviewAtomicClock{}
	clock.n.Store(1000)
	s, err := store.OpenWithClock(filepath.Join(t.TempDir(), "db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid, did := "01K8WMXZF57CGNZKX1FZ38DZMA", "01K8WMXZF57CGNZKX1FZ38DZMB"
	version := "8.3"
	if _, err = s.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: did, FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, nil, &fakeAudit{})
	q.SetClock(clock)
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &php.SetRequest{SiteID: sid, Version: version}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.LoadOperation(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	clock.n.Store(op.Expires)
	if err = q.expireOperations(ctx); !errors.Is(err, store.ErrOperationLocked) {
		t.Fatalf("missing expired journal block: %v", err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.execute(ctx, j); err == nil {
		t.Fatal("latched queue executed another job")
	}
	unchanged, err := s.GetJob(ctx, r.Job.ID)
	if err != nil || unchanged.State != "queued" {
		t.Fatalf("latched job was started: %+v %v", unchanged, err)
	}
	if _, err = q.Enqueue(ctx, "reconcile", map[string]any{}, "system"); err == nil {
		t.Fatal("queue admitted new work after expiry recovery refusal")
	}
}

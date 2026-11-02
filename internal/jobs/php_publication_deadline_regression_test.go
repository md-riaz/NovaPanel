package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"novapanel/internal/audit"
	php "novapanel/internal/render/php"
	"novapanel/internal/store"
)

func TestPHPExpiryDuringAuditKeepsQueueClosed(t *testing.T) {
	clock := &reviewAtomicClock{}
	clock.n.Store(1000)
	s, err := store.OpenWithClock(filepath.Join(t.TempDir(), "panel.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := &publicationAuditFixture{fakeAudit: &fakeAudit{}, s: s}
	q := NewQueue(s, nil, a)
	q.SetClock(clock)
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	sid, did := "01K8WMXZF57CGNZKX1FZ38DZMA", "01K8WMXZF57CGNZKX1FZ38DZMB"
	version := "8.3"
	if _, err = s.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: did, FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	memory := 512
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &php.SetRequest{SiteID: sid, Version: version, Limits: php.LimitsPatch{MemoryMB: &memory}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.LoadOperation(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	a.before = func(_ string, ev audit.Event) error {
		if ev.Detail["publication"] == "intent" {
			clock.n.Store(op.Expires)
		}
		return nil
	}
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if err := s.CheckpointOperation(ctx, j.ID, "prepared", "config_published"); err != nil {
			return err
		}
		return s.CheckpointOperation(ctx, j.ID, "config_published", "verified")
	})
	err = q.execute(ctx, r.Job)
	if !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("published operation must block on audit-deadline crossing: %v", err)
	}
	if q.healthy() == nil {
		t.Fatal("queue remained healthy after ambiguous published operation")
	}
	after, err := s.LoadOperation(ctx, r.Job.ID)
	if err != nil || after.Checkpoint != "verified" {
		t.Fatalf("recovery lost: %+v %v", after, err)
	}
}

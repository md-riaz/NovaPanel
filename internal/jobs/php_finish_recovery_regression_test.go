package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	php "novapanel/internal/render/php"
	"novapanel/internal/store"
)

func TestPHPFinishEligibilityFailurePreservesRecovery(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
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
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	memory := 512
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &php.SetRequest{SiteID: sid, Version: version, Limits: php.LimitsPatch{MemoryMB: &memory}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if err := s.CheckpointOperation(ctx, j.ID, "prepared", "config_published"); err != nil {
			return err
		}
		if err := s.CheckpointOperation(ctx, j.ID, "config_published", "verified"); err != nil {
			return err
		}
		return s.SetSiteStatus(ctx, sid, "failed")
	})
	err = q.execute(ctx, r.Job)
	if !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("expected recovery blocker after published effect, got %v", err)
	}
	op, err := s.LoadOperation(ctx, r.Job.ID)
	if err != nil || op.Checkpoint != "verified" {
		t.Fatalf("recovery journal lost: %+v %v", op, err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil || j.State != "running" {
		t.Fatalf("job must remain recoverable: %+v %v", j, err)
	}
	if _, err := q.Enqueue(ctx, "reconcile", map[string]any{}, "system"); err == nil {
		t.Fatal("unrelated mutation admitted after unresolved PHP publication")
	}
}

package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"novapanel/internal/render/nginx"
	php "novapanel/internal/render/php"
	"novapanel/internal/store"
)

func TestPHPRecoveryRequiredClosesQueueAdmission(t *testing.T) {
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
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
	if err = q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	memory := 512
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &php.SetRequest{SiteID: sid, Version: version, Limits: php.LimitsPatch{MemoryMB: &memory}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(context.Context, *Job) error { return nginx.ErrPHPRecoveryRequired })
	otherSite := "01K8WMXZF57CGNZKX1FZ38DZMD"
	if _, err = s.CreateSite(ctx, store.Site{ID: otherSite, Name: "other", Type: "static", Docroot: "/srv/sites/other/public"}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZME", FQDN: "other.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateDatabasePrimary(ctx, store.DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMF", UserID: "01K8WMXZF57CGNZKX1FZ38DZMG", SiteID: otherSite}); err != nil {
		t.Fatal(err)
	}
	other, err := q.Enqueue(ctx, "site.delete", map[string]any{}, "site:"+otherSite)
	if err != nil {
		t.Fatal(err)
	}
	if err = q.execute(ctx, r.Job); !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("expected recovery block: %v", err)
	}
	if err = q.execute(ctx, other.Job); err == nil {
		t.Fatal("latched queue processed unsupported job")
	}
	unchanged, err := s.GetJob(ctx, other.Job.ID)
	if err != nil || unchanged.State != "queued" {
		t.Fatalf("latched job state changed: %+v %v", unchanged, err)
	}
	if err = q.PrepareStartup(ctx); err == nil {
		t.Fatal("startup reported ready after unresolved PHP recovery")
	}
	if _, err = q.Enqueue(ctx, "reconcile", map[string]any{}, "system"); err == nil {
		t.Fatal("queue admitted mutation after PHP recovery refusal")
	}
}

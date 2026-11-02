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
	memory := 512
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &php.SetRequest{SiteID: sid, Version: version, Limits: php.LimitsPatch{MemoryMB: &memory}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.SetRunner(func(context.Context, *Job) error { return nginx.ErrPHPRecoveryRequired })
	if err = q.execute(ctx, r.Job); !errors.Is(err, ErrRecoveryBlocked) {
		t.Fatalf("expected recovery block: %v", err)
	}
	if _, err = q.Enqueue(ctx, "reconcile", map[string]any{}, "system"); err == nil {
		t.Fatal("queue admitted mutation after PHP recovery refusal")
	}
}

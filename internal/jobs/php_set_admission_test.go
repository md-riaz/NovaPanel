package jobs

import (
	"context"
	"path/filepath"
	"testing"

	php "novapanel/internal/render/php"
	"novapanel/internal/store"
)

func TestPHPSetAdmissionPersistsCanonicalRequestAndSnapshot(t *testing.T) {
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
	req := php.SetRequest{SiteID: sid, Version: version, Limits: php.LimitsPatch{MemoryMB: &memory}}
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &req}, nil)
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := php.DecodeSetRequest(j.Args)
	if err != nil || decoded.SiteID != sid || *decoded.Limits.MemoryMB != 512 || j.ObjectKey != "site:"+sid || r.ClaimToken != "" {
		t.Fatalf("job: %+v decoded=%+v err=%v", j, decoded, err)
	}
	op, err := s.LoadOperation(ctx, j.ID)
	if err != nil || op.Identity.PHP.DesiredLimits.MemoryMB != 512 {
		t.Fatalf("operation: %+v %v", op, err)
	}
}

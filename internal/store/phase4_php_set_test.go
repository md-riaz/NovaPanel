package store

import (
	"context"
	php "novapanel/internal/render/php"
	"path/filepath"
	"testing"
)

func TestPHPSetJournalReopenAndAtomicTerminal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	sid := "01K8WMXZF57CGNZKX1FZ38DZMA"
	did := "01K8WMXZF57CGNZKX1FZ38DZMB"
	jid := "01K8WMXZF57CGNZKX1FZ38DZMC"
	version := "8.3"
	_, err = s.CreateSite(ctx, Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, Domain{ID: did, FQDN: "demo.example.test"})
	if err != nil {
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
	if op.Identity.PHP.DesiredLimits != (php.Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}) {
		t.Fatalf("desired: %+v", op.Identity.PHP)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	op, err = s.LoadOperation(ctx, jid)
	if err != nil || op.Identity.PHP.DesiredLimits.MemoryMB != 512 {
		t.Fatalf("reopen: %+v %v", op, err)
	}
	if _, err = s.StartJob(ctx, jid, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperation(ctx, jid, "prepared", "config_published"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperation(ctx, jid, "config_published", "verified"); err != nil {
		t.Fatal(err)
	}
	phase4Exec(t, s.db.raw, `CREATE TRIGGER fail_php BEFORE UPDATE OF state ON jobs WHEN NEW.state='done' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err = s.CompleteOperation(ctx, jid, nil); err == nil {
		t.Fatal("terminal failure ignored")
	}
	op, err = s.LoadOperation(ctx, jid)
	if err != nil || op.Checkpoint != "verified" {
		t.Fatalf("journal: %+v %v", op, err)
	}
	if _, err = s.GetPHPLimits(ctx, sid); err == nil {
		t.Fatal("metadata committed despite failure")
	}
	phase4Exec(t, s.db.raw, `DROP TRIGGER fail_php`)
	if err = s.CompleteOperation(ctx, jid, nil); err != nil {
		t.Fatal(err)
	}
	limits, err := s.GetPHPLimits(ctx, sid)
	if err != nil || limits.MemoryMB != 512 {
		t.Fatalf("limits: %+v %v", limits, err)
	}
	job, err := s.GetJob(ctx, jid)
	if err != nil || job.State != "done" || string(job.Result) != "{}" {
		t.Fatalf("job: %+v %v", job, err)
	}
}

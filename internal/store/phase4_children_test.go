package store

import (
	"context"
	"testing"

	php "novapanel/internal/render/php"
)

func TestOperationChildrenAtomicJournalAndReplay(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	phase4Job(t, s.db.raw, "child-job", "running")
	phase4Exec(t, s.db.raw, `INSERT INTO phase4_operations(job_id,kind,checkpoint,identities_json,expires,updated) VALUES('child-job','site_update','prepared','{}',9999999999999,1)`)
	children := []OperationChild{{Ordinal: 0, Kind: "maintenance_on"}, {Ordinal: 1, Kind: "maintenance_off"}}
	if err = s.PrepareOperationChildren(ctx, "child-job", children); err != nil {
		t.Fatal(err)
	}
	if err = s.EngageOperationBracket(ctx, "child-job"); err != nil {
		t.Fatal(err)
	}
	phase4Exec(t, s.db.raw, `CREATE TRIGGER reject_child_event BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err = s.CommitOperationChild(ctx, "child-job", 0, "verified", "committed", ""); err == nil {
		t.Fatal("journal failure accepted")
	}
	got, err := s.OperationChildren(ctx, "child-job")
	if err != nil || got[0].Status != "pending" || !got[0].BracketEngaged {
		t.Fatalf("partial commit: %+v %v", got, err)
	}
	phase4Exec(t, s.db.raw, `DROP TRIGGER reject_child_event`)
	if err = s.CommitOperationChild(ctx, "child-job", 0, "verified", "committed", ""); err != nil {
		t.Fatal(err)
	}
	phase4Refuse(t, s.db.raw, `UPDATE phase4_operation_children SET checkpoint='prepared' WHERE job_id='child-job' AND ordinal=0`, "cannot rewind")
	phase4Refuse(t, s.db.raw, `UPDATE phase4_operation_children SET bracket_engaged=0 WHERE job_id='child-job' AND ordinal=0`, "cannot rewind")
	got, err = s.OperationChildren(ctx, "child-job")
	if err != nil || got[0].Status != "committed" || got[0].JournalRef == "" {
		t.Fatalf("commit: %+v %v", got, err)
	}
}

func TestPHPChildAtomicMetadata(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid := "01K8WMXZF57CGNZKX1FZ38DZMA"
	version := "8.3"
	if _, err = s.CreateSite(ctx, Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	phase4Job(t, s.db.raw, "php-child-job", "running")
	phase4Exec(t, s.db.raw, `INSERT INTO phase4_operations(job_id,kind,site_id,checkpoint,identities_json,expires,updated) VALUES('php-child-job','site_update',?,'prepared','{}',9999999999999,1)`, sid)
	p := &PHPOperationIdentity{OriginalVersion: version, DesiredVersion: version, OriginalCap: 2, DesiredCap: 2, OriginalLimits: php.Limits{MemoryMB: 256, UploadMB: 64, MaxExecSec: 60}, DesiredLimits: php.Limits{MemoryMB: 512, UploadMB: 64, MaxExecSec: 60}}
	if err = s.PrepareOperationChildren(ctx, "php-child-job", []OperationChild{{Kind: "php_change", Identity: OperationChildIdentity{PHP: p}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperationChild(ctx, "php-child-job", 0, "prepared", "config_published"); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperationChild(ctx, "php-child-job", 0, "config_published", "verified"); err != nil {
		t.Fatal(err)
	}
	phase4Exec(t, s.db.raw, `CREATE TRIGGER reject_php_child_event BEFORE INSERT ON job_events BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err = s.CommitPHPChild(ctx, "php-child-job", 0); err == nil {
		t.Fatal("journal failure accepted")
	}
	if _, err = s.GetPHPLimits(ctx, sid); err == nil {
		t.Fatal("metadata escaped failed transaction")
	}
	phase4Exec(t, s.db.raw, `DROP TRIGGER reject_php_child_event`)
	if err = s.CommitPHPChild(ctx, "php-child-job", 0); err != nil {
		t.Fatal(err)
	}
	limits, err := s.GetPHPLimits(ctx, sid)
	if err != nil || limits.MemoryMB != 512 {
		t.Fatalf("limits: %+v %v", limits, err)
	}
	job, err := s.GetJob(ctx, "php-child-job")
	if err != nil || job.State != "running" {
		t.Fatalf("parent: %+v %v", job, err)
	}
}

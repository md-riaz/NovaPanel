package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSiteDeleteJournalSnapshotAndAtomicRetention(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	phase4Exec(t, s.db.raw, "INSERT INTO sites(id,name,type,docroot,status,created,updated) VALUES('site','example','wordpress','/scratch/example','active',1,1)")
	pair := DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", SiteID: "site"}
	if err = s.CreateDatabasePrimary(ctx, pair); err != nil {
		t.Fatal(err)
	}
	phase4Exec(t, s.db.raw, "INSERT INTO wordpress_bindings(site_id,db_id) VALUES('site',?)", pair.ID)
	j := Job{ID: "delete", Verb: "site.delete", CreatedBy: "session-owner", Role: "owner", State: "queued", Args: []byte(`{}`)}
	op, err := s.PrepareOperation(ctx, j, JobClaim{}, OperationRequest{Kind: SiteDelete, SiteID: "site"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Identity.Attached) != 1 || op.Identity.BindingID != pair.ID || op.Identity.Site.Name != "example" {
		t.Fatalf("snapshot: %+v", op)
	}
	if !reflect.DeepEqual(op.Keys(), []string{"site:site", "db:" + pair.ID}) {
		t.Fatalf("keys: %v", op.Keys())
	}
	phase4Exec(t, s.db.raw, "UPDATE sites SET docroot='/scratch/changed' WHERE id='site'")
	if err = s.CheckOperation(ctx, j.ID); !errors.Is(err, ErrSiteConflict) {
		t.Fatalf("stale site: %v", err)
	}
	phase4Exec(t, s.db.raw, "UPDATE sites SET docroot='/scratch/example' WHERE id='site'")
	if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
		t.Fatal(err)
	}
	phase4Exec(t, s.db.raw, `CREATE TRIGGER fail_site_done BEFORE UPDATE OF checkpoint ON phase4_operations WHEN NEW.checkpoint='done' BEGIN SELECT RAISE(ABORT,'injected'); END`)
	if err = s.CompleteOperation(ctx, j.ID, nil); err == nil {
		t.Fatal("terminal failure ignored")
	}
	if _, err = s.SiteByID(ctx, "site"); err != nil {
		t.Fatal("site removal escaped rollback", err)
	}
	db, err := s.GetDatabase(ctx, pair.ID)
	if err != nil || db.SiteID != "site" {
		t.Fatalf("detach escaped rollback: %+v %v", db, err)
	}
	phase4Exec(t, s.db.raw, "DROP TRIGGER fail_site_done")
	if err = s.CompleteOperation(ctx, j.ID, nil); err != nil {
		t.Fatal(err)
	}
	db, err = s.GetDatabase(ctx, pair.ID)
	if err != nil || db.SiteID != "" {
		t.Fatalf("retention: %+v %v", db, err)
	}
	if err = s.CompleteOperation(ctx, j.ID, nil); err != nil {
		t.Fatal("done replay", err)
	}
}

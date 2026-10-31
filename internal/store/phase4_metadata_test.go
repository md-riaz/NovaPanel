package store

import (
	"context"
	"errors"
	php "novapanel/internal/render/php"
	"path/filepath"
	"testing"
)

func TestPhase4TypedMetadata(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	phase4Exec(t, s.db.raw, "INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('php','phpone','php','/scratch/php',1,1),('static','staticone','static','/scratch/static',1,1)")
	if _, err = s.MergePHPLimits(ctx, "static", php.LimitsPatch{}); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("static accepted: %v", err)
	}
	got, err := s.MergePHPLimits(ctx, "php", php.LimitsPatch{})
	if err != nil || got.MemoryMB != 256 {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	memory := 32
	if _, err = s.MergePHPLimits(ctx, "php", php.LimitsPatch{MemoryMB: &memory}); err == nil {
		t.Fatal("invalid merged limits accepted")
	}
	got, err = s.GetPHPLimits(ctx, "php")
	if err != nil || got.MemoryMB != 256 {
		t.Fatalf("failed update changed values: %+v %v", got, err)
	}
	pair := DatabasePrimary{ID: "01K8WMXZF57CGNZKX1FZ38DZMQ", UserID: "01K8WMXZF57CGNZKX1FZ38DZMR", SiteID: "php"}
	if err = s.CreateDatabasePrimary(ctx, pair); err != nil {
		t.Fatal(err)
	}
	db, err := s.GetDatabase(ctx, pair.ID)
	if err != nil || db.PrimaryUserID != pair.UserID || db.Name != "d_01k8wmxzf57cgnzkx1fz38dzmq" {
		t.Fatalf("primary projection: %+v %v", db, err)
	}
	if err = s.CreateDatabasePrimary(ctx, pair); !errors.Is(err, ErrSiteConflict) {
		t.Fatalf("collision adopted: %v", err)
	}
}

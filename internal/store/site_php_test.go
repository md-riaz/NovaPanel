package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestSitePHPDesiredState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('p','php-site','php','/srv/sites/php-site/public',1,1),('s','static-site','static','/srv/sites/static-site/public',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	memory, upload := 256, 32
	if err = s.SetSitePHP(ctx, "p", "8.3", PHPLimits{MemoryMB: &memory, UploadMB: &upload}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSitePHP(ctx, "s", "8.3", PHPLimits{}); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("static: %v", err)
	}
	if err = s.SetSitePHP(ctx, "missing", "8.3", PHPLimits{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	site, err := s.SiteByID(ctx, "p")
	if err != nil || site.PHPVersion == nil || *site.PHPVersion != "8.3" {
		t.Fatalf("version: %+v %v", site, err)
	}
	limits, err := s.SitePHPLimits(ctx, "p")
	if err != nil || limits.MemoryMB == nil || *limits.MemoryMB != memory || limits.UploadMB == nil || *limits.UploadMB != upload || limits.MaxExecSec != nil {
		t.Fatalf("limits: %+v %v", limits, err)
	}
	if err = s.SetSiteStatus(ctx, "p", "deleting"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSite(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM settings WHERE key=?", phpLimitsKey("p")).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cleanup: %d %v", count, err)
	}
}

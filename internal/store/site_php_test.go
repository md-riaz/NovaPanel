package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSiteCreatePersistsCapAndRetryKeepsIt(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	v := Site{ID: "site-a", Name: "alpha", Type: "php", Docroot: "/srv/sites/alpha/public"}
	d := Domain{ID: "domain-a", FQDN: "alpha.test"}
	cap := 7
	if _, err = s.CreateSite(ctx, v, d, PHPLimits{MaxChildren: &cap}); err != nil {
		t.Fatal(err)
	}
	cap = 9
	if _, err = s.CreateSite(ctx, v, d, PHPLimits{MaxChildren: &cap}); err != nil {
		t.Fatal(err)
	}
	limits, err := s.SitePHPLimits(ctx, v.ID)
	if err != nil || limits.MaxChildren == nil || *limits.MaxChildren != 7 {
		t.Fatalf("cap changed: %+v %v", limits, err)
	}
}

func TestBackfillSitePHPCapOnce(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.db.ExecContext(ctx, `INSERT INTO sites(id,name,type,status,docroot,created,updated) VALUES('p','php-site','php','active','/p',1,1),('s','static-site','static','active','/s',1,1),('n','new-site','php','creating','/n',1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value_json) VALUES(?,?)`, phpLimitsKey("p"), `{"memory_mb":128}`)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	compute := func(active int) int {
		calls++
		if active != 2 {
			t.Fatalf("active sites = %d", active)
		}
		return 7
	}
	for i := 0; i < 2; i++ {
		limits, err := s.BackfillSitePHPCap(ctx, "p", compute)
		if err != nil || limits.MaxChildren == nil || *limits.MaxChildren != 7 || limits.MemoryMB == nil || *limits.MemoryMB != 128 {
			t.Fatalf("limits %+v: %v", limits, err)
		}
	}
	if calls != 1 {
		t.Fatalf("computed %d times", calls)
	}
	if _, err = s.BackfillSitePHPCap(ctx, "s", compute); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("static: %v", err)
	}
	if _, err = s.BackfillSitePHPCap(ctx, "n", compute); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("creating: %v", err)
	}
}

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
	memory, upload, cap := 256, 32, 7
	wire, err := json.Marshal(PHPLimits{MaxChildren: &cap})
	if err != nil || strings.Contains(string(wire), "max_children") {
		t.Fatalf("cap exposed on RPC: %s %v", wire, err)
	}
	if err = s.SetSitePHP(ctx, "p", "8.3", PHPLimits{MemoryMB: &memory, UploadMB: &upload, MaxChildren: &cap}); err != nil {
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
	if err != nil || limits.MemoryMB == nil || *limits.MemoryMB != memory || limits.UploadMB == nil || *limits.UploadMB != upload || limits.MaxExecSec != nil || limits.MaxChildren == nil || *limits.MaxChildren != cap {
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

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func siteReadStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestTypedRedirectReads(t *testing.T) {
	s, _ := siteReadStore(t)
	ctx := context.Background()
	empty, err := s.ListRedirects(ctx, "missing")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty: %#v %v", empty, err)
	}
	for _, statement := range []string{
		`INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site','example','static','/srv/sites/example/public',1,1),('other','other','static','/srv/sites/other/public',1,1)`,
		`INSERT INTO redirects(id,site_id,source,target,code) VALUES('b','site','/temp','https://example.test/new',302),('a','site','/old','/new',301),('c','other','/old','/else',301)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	want := []Redirect{{ID: "a", SiteID: "site", Source: "/old", Target: "/new", Code: 301}, {ID: "b", SiteID: "site", Source: "/temp", Target: "https://example.test/new", Code: 302}}
	got, err := s.ListRedirects(ctx, "site")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("redirects: %#v %v", got, err)
	}
	for _, v := range want {
		got, err := s.RedirectByID(ctx, v.ID)
		if err != nil || !reflect.DeepEqual(got, v) {
			t.Fatalf("redirect: %#v %v", got, err)
		}
	}
	if _, err := s.RedirectByID(ctx, "' OR 1=1 --"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing: %v", err)
	}
	got, err = s.ListRedirects(ctx, "' OR 1=1 --")
	if err != nil || len(got) != 0 {
		t.Fatalf("filter: %#v %v", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ListRedirects(canceled, "site"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestTypedDomainReads(t *testing.T) {
	s, _ := siteReadStore(t)
	ctx := context.Background()
	empty, err := s.ListDomains(ctx, "missing")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty: %#v %v", empty, err)
	}
	for _, statement := range []string{
		`INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site','example','static','/srv/sites/example/public',1,1),('other','other','static','/srv/sites/other/public',1,1)`,
		`INSERT INTO domains(id,site_id,fqdn,is_primary,cert_state,cert_staging,cert_not_after,created) VALUES('a','site','example.test',1,'issued',1,123456,2)`,
		`INSERT INTO domains(id,site_id,fqdn,created) VALUES('b','site','www.example.test',3),('c','other','other.test',4)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	expires := int64(123456)
	want := []Domain{
		{ID: "a", SiteID: "site", FQDN: "example.test", IsPrimary: true, CertState: "issued", CertStaging: true, CertNotAfter: &expires, Created: 2},
		{ID: "b", SiteID: "site", FQDN: "www.example.test", CertState: "none", Created: 3},
	}
	got, err := s.ListDomains(ctx, "site")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("domains: %#v %v", got, err)
	}
	for _, v := range want {
		got, err := s.DomainByID(ctx, v.ID)
		if err != nil || !reflect.DeepEqual(got, v) {
			t.Fatalf("domain: %#v %v", got, err)
		}
	}
	if _, err := s.DomainByID(ctx, "' OR 1=1 --"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing: %v", err)
	}
	got, err = s.ListDomains(ctx, "' OR 1=1 --")
	if err != nil || len(got) != 0 {
		t.Fatalf("filter: %#v %v", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ListDomains(canceled, "site"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestTypedSiteReads(t *testing.T) {
	s, path := siteReadStore(t)
	ctx := context.Background()
	empty, err := s.ListSites(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty: %#v %v", empty, err)
	}
	for _, statement := range []string{
		`INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('b','second','static','/srv/sites/second/public',10,20)`,
		`INSERT INTO sites(id,name,type,docroot,php_version,force_https,hsts_max_age,cert_source,ssl_auto,maintenance,config_hash,created,updated) VALUES('a','first','php','/srv/sites/first/public','8.3',1,300,'manual',0,1,'hash',11,21)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sites, err := s.ListSites(ctx)
	if err != nil || len(sites) != 2 {
		t.Fatalf("sites: %#v %v", sites, err)
	}
	php, hash := "8.3", "hash"
	want := Site{ID: "a", Name: "first", Type: "php", StackDriver: "nginx", Docroot: "/srv/sites/first/public", PHPVersion: &php, ForceHTTPS: true, HSTSMaxAge: 300, CertSource: "manual", SSLAuto: false, Maintenance: true, ConfigHash: &hash, Created: 11, Updated: 21}
	if !reflect.DeepEqual(sites[0], want) {
		t.Fatalf("site: %#v", sites[0])
	}
	wantDefault := Site{ID: "b", Name: "second", Type: "static", StackDriver: "nginx", Docroot: "/srv/sites/second/public", CertSource: "none", SSLAuto: true, Created: 10, Updated: 20}
	if !reflect.DeepEqual(sites[1], wantDefault) {
		t.Fatalf("default: %#v", sites[1])
	}
	byID, err := s.SiteByID(ctx, "a")
	if err != nil || !reflect.DeepEqual(byID, want) {
		t.Fatalf("by ID: %#v %v", byID, err)
	}
	byName, err := s.SiteByName(ctx, "first")
	if err != nil || !reflect.DeepEqual(byName, want) {
		t.Fatalf("by name: %#v %v", byName, err)
	}
	for _, lookup := range []func(context.Context, string) (Site, error){s.SiteByID, s.SiteByName} {
		for _, key := range []string{"missing", "' OR 1=1 --", ""} {
			if _, err := lookup(ctx, key); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("missing %q: %v", key, err)
			}
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ListSites(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SiteByID(ctx, "a"); err == nil {
		t.Fatal("read after close")
	}
}

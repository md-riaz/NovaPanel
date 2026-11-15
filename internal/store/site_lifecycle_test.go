package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSiteCreateLifecycle(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	want := Site{ID: "site-a", Name: "alpha", Type: "static", Docroot: "/srv/sites/alpha/public"}
	domain := Domain{ID: "domain-a", FQDN: "alpha.test"}
	got, err := s.CreateSite(ctx, want, domain)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "creating" || got.StackDriver != "nginx" || got.CertSource != "none" || !got.SSLAuto {
		t.Fatalf("defaults: %+v", got)
	}
	for _, status := range []string{"failed", "active", "deleting"} {
		if err = s.SetSiteStatus(ctx, got.ID, status); err != nil {
			t.Fatal(err)
		}
		read, e := s.SiteByID(ctx, got.ID)
		if e != nil || read.Status != status {
			t.Fatalf("status %s: %+v %v", status, read, e)
		}
		want.ID = "other-id"
		domain.ID = "other-domain"
		again, e := s.CreateSite(ctx, want, domain)
		if status == "deleting" {
			if !errors.Is(e, ErrSitePrecondition) {
				t.Fatal(e)
			}
		} else if e != nil || again.ID != got.ID || again.Status != status {
			t.Fatalf("ensure: %+v %v", again, e)
		}
	}
	if err = s.SetSiteStatus(ctx, got.ID, "invalid"); !errors.Is(err, ErrSiteInvalid) {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, "missing", "active"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestSiteCreateRollback(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	a := Site{ID: "a", Name: "alpha", Type: "static", Docroot: "/srv/sites/alpha/public"}
	d := Domain{ID: "d", FQDN: "alpha.test"}
	if _, err = s.CreateSite(ctx, a, d); err != nil {
		t.Fatal(err)
	}
	a.ID = "b"
	a.Name = "bravo"
	a.Docroot = "/srv/sites/bravo/public"
	d.ID = "e"
	if _, err = s.CreateSite(ctx, a, d); !errors.Is(err, ErrSiteConflict) {
		t.Fatal(err)
	}
	if _, err = s.SiteByID(ctx, "b"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("partial site persisted", err)
	}
	a.ID = "a"
	a.Name = "alpha"
	a.Docroot = "/wrong"
	if _, err = s.CreateSite(ctx, a, d); !errors.Is(err, ErrSiteConflict) {
		t.Fatal(err)
	}
}

func TestSiteLifecycleSchema(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err = s.db.ExecContext(ctx, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('a','alpha','static','/srv/sites/alpha/public',1,1)`); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = s.db.QueryRowContext(ctx, `SELECT status FROM sites WHERE id='a'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "creating" {
		t.Fatal(status)
	}
	for _, value := range []string{"active", "failed", "deleting"} {
		if _, err = s.db.ExecContext(ctx, `UPDATE sites SET status=? WHERE id='a'`, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE sites SET status='unknown' WHERE id='a'`); err == nil {
		t.Fatal("invalid lifecycle accepted")
	}
}

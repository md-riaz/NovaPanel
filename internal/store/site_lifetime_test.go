package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestSiteLifetimeDisposition(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db := s.db.raw
	phase4Exec(t, db, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('old','old','php','/old',1,1)`)
	if phase4Count(t, db, `SELECT count(*) FROM sites WHERE id='old' AND lifetime_state='served'`) != 1 {
		t.Fatal("unsafe default")
	}
	phase4Refuse(t, db, `UPDATE sites SET lifetime_state='provisioning' WHERE id='old'`, "site lifetime cannot rewind")
	phase4Exec(t, db, `INSERT INTO sites(id,name,type,docroot,created,updated,lifetime_state) VALUES('fresh','fresh','wordpress','/fresh',1,1,'provisioning')`)
	phase4Exec(t, db, `UPDATE sites SET lifetime_state='served' WHERE id='fresh'`)
	phase4Refuse(t, db, `UPDATE sites SET lifetime_state='provisioning' WHERE id='fresh'`, "site lifetime cannot rewind")
}

func TestFreshSiteLifetimeIdentityAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db := s.db.raw
	phase4Job(t, db, "allocation", "running")
	phase4Exec(t, db, `INSERT INTO sites(id,name,type,docroot,created,updated,lifetime_state) VALUES('fresh','example','wordpress','/sites/example/public',1,1,'provisioning')`)
	phase4Exec(t, db, `INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES('fresh','example',123,456,'allocation','owned')`)
	identity := SiteLifetime{SiteID: "fresh", Name: "example", Docroot: "/sites/example/public", AllocatingJob: "allocation", UID: 123, GID: 456}
	if err := s.CheckFreshSiteLifetime(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CheckFreshSiteLifetime(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	changed := identity
	changed.UID++
	if err := s.CheckFreshSiteLifetime(context.Background(), changed); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("changed identity: %v", err)
	}
	if err := s.ActivateSiteLifetime(context.Background(), changed); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("changed activation: %v", err)
	}
	if err := s.ActivateSiteLifetime(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckFreshSiteLifetime(context.Background(), identity); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("served: %v", err)
	}
	if err := s.ActivateSiteLifetime(context.Background(), identity); !errors.Is(err, ErrSitePrecondition) {
		t.Fatalf("repeat: %v", err)
	}
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestReserveSiteAccountTx(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for i, name := range []string{"first", "second"} {
		phase4Job(t, s.db.raw, name, "queued")
		phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES(?,?,'wordpress',?,1,1)`, name, name, "/"+name)
		var uid uint32
		err = s.Tx(ctx, func(tx *sql.Tx) error {
			var e error
			uid, e = s.ReserveSiteAccountTx(ctx, tx, name, name, name)
			return e
		})
		if err != nil || uid != uint32(siteUIDMin+i) {
			t.Fatalf("allocation %d: uid=%d err=%v", i, uid, err)
		}
	}
	phase4Refuse(t, s.db.raw, `DELETE FROM site_account_provenance WHERE site_id='first'`, "retained provenance")
}

func TestReserveWordPressSiteTx(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	v := Site{ID: "site", Name: "example", Type: "wordpress", Docroot: "/sites/example/public"}
	d := Domain{ID: "domain", FQDN: "example.test"}
	reserve := func() error {
		return s.Tx(ctx, func(tx *sql.Tx) error { _, err := s.ReserveWordPressSiteTx(ctx, tx, v, d, "allocation"); return err })
	}
	if err := reserve(); err == nil {
		t.Fatal("reservation without job succeeded")
	}
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM sites WHERE id='site'`) != 0 {
		t.Fatal("failed reservation leaked site")
	}
	phase4Job(t, s.db.raw, "allocation", "queued")
	if err := reserve(); err != nil {
		t.Fatal(err)
	}
	if phase4Count(t, s.db.raw, `SELECT count(*) FROM sites s JOIN site_account_provenance p ON p.site_id=s.id WHERE s.id='site' AND s.status='creating' AND s.lifetime_state='provisioning' AND p.allocating_job='allocation' AND p.uid=2000000 AND p.gid=2000000`) != 1 {
		t.Fatal("incorrect admission state")
	}
	if err := reserve(); !errors.Is(err, ErrSiteConflict) {
		t.Fatalf("duplicate reservation: %v", err)
	}
}

func TestDeleteRetiresSiteAccount(t *testing.T) {
	for _, kind := range []string{"php", "wordpress"} {
		t.Run(kind, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			phase4Job(t, s.db.raw, "allocation", "queued")
			phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site','example',?,'/site',1,1)`, kind)
			phase4Exec(t, s.db.raw, `INSERT INTO site_account_provenance(site_id,account_name,uid,gid,allocating_job,evidence) VALUES('site','example',2000000,2000000,'allocation','owned')`)
			phase4Refuse(t, s.db.raw, `UPDATE site_account_provenance SET site_id=NULL WHERE site_id='site'`, "immutable provenance")
			err = s.Tx(context.Background(), func(tx *sql.Tx) error { return completeSiteDelete(context.Background(), tx, Operation{SiteID: "site"}) })
			if err != nil {
				t.Fatal(err)
			}
			if phase4Count(t, s.db.raw, `SELECT count(*) FROM site_account_provenance WHERE site_id IS NULL AND uid=2000000 AND gid=2000000`) != 1 {
				t.Fatal("provenance not retained")
			}
			phase4Refuse(t, s.db.raw, `DELETE FROM site_account_provenance WHERE uid=2000000`, "retained provenance")
			phase4Job(t, s.db.raw, "allocation2", "queued")
			phase4Exec(t, s.db.raw, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site2','example','wordpress','/site2',1,1)`)
			var uid uint32
			err = s.Tx(context.Background(), func(tx *sql.Tx) error {
				var e error
				uid, e = s.ReserveSiteAccountTx(context.Background(), tx, "site2", "example", "allocation2")
				return e
			})
			if err != nil || uid != 2000001 {
				t.Fatalf("retired UID reused: %d, %v", uid, err)
			}
		})
	}
}

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

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSiteSchemaPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	ctx := context.Background()
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site-a','example','static','/srv/sites/example/public',10,10)`); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `INSERT INTO domains(id,site_id,fqdn,is_primary,created) VALUES('domain-a','site-a','example.test',1,10)`); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO redirects(id,site_id,source,target,code) VALUES('redirect-a','site-a','/old','/new',301)`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var driver, source string
	var php, hash sql.NullString
	var force, hsts, auto, maintenance int
	if err = s.db.QueryRowContext(ctx, `SELECT stack_driver,php_version,force_https,hsts_max_age,cert_source,ssl_auto,maintenance,config_hash FROM sites WHERE id='site-a'`).Scan(&driver, &php, &force, &hsts, &source, &auto, &maintenance, &hash); err != nil {
		t.Fatal(err)
	}
	if driver != "nginx" || php.Valid || hash.Valid || force != 0 || hsts != 0 || source != "none" || auto != 1 || maintenance != 0 {
		t.Fatalf("unexpected static defaults: %q %v %d %d %q %d %d %v", driver, php, force, hsts, source, auto, maintenance, hash)
	}
	var cert string
	var staging int
	var expires sql.NullInt64
	if err = s.db.QueryRowContext(ctx, `SELECT cert_state,cert_staging,cert_not_after FROM domains WHERE id='domain-a'`).Scan(&cert, &staging, &expires); err != nil {
		t.Fatal(err)
	}
	if cert != "none" || staging != 0 || expires.Valid {
		t.Fatal("unexpected certificate defaults")
	}
	for _, statement := range []string{
		`INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site-b','example','static','/srv/sites/example/public',10,10)`,
		`INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site-b','other','unsafe','/srv/sites/other/public',10,10)`,
		`UPDATE sites SET cert_source='unsafe' WHERE id='site-a'`,
		`INSERT INTO domains(id,site_id,fqdn,created) VALUES('domain-b','site-a','example.test',10)`,
		`INSERT INTO domains(id,site_id,fqdn,created) VALUES('domain-b','missing','other.test',10)`,
		`UPDATE domains SET cert_state='unsafe' WHERE id='domain-a'`,
		`INSERT INTO redirects(id,site_id,source,target,code) VALUES('redirect-b','site-a','/bad','/new',307)`,
	} {
		if _, err = s.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("accepted invalid metadata: %s", statement)
		}
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM sites WHERE id='site-a'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sites", "domains", "redirects"} {
		var count int
		if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cascade %s: %d %v", table, count, err)
		}
	}
}

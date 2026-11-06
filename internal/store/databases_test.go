package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDatabaseMetadataPersistsAndDetaches(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sites(id,name,type,docroot,created,updated) VALUES('site-a','example','static','/srv/sites/example/public',1,1)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO databases(id,site_id,name,created) VALUES('db-a','site-a','d_example',1)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO db_users(id,db_id,username,created) VALUES('user-a','db-a','u_example',1)`)
		return err
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
	defer s.Close()
	list, err := s.ListDatabases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].SiteID == nil || *list[0].SiteID != "site-a" || list[0].Engine != "mariadb" {
		t.Fatalf("unexpected list: %+v", list)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM sites WHERE id='site-a'`); err != nil {
		t.Fatal(err)
	}
	list, err = s.ListDatabases(ctx)
	if err != nil || len(list) != 1 || list[0].SiteID != nil {
		t.Fatalf("detach: %+v %v", list, err)
	}
	var host string
	if err = s.db.QueryRowContext(ctx, `SELECT host FROM db_users WHERE id='user-a'`).Scan(&host); err != nil || host != "127.0.0.1" {
		t.Fatalf("host: %q %v", host, err)
	}
}

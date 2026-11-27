package store

import (
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

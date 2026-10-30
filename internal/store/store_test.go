package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenMigrateReopenIntegrity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err = s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL %q %v", mode, err)
	}
	var fk, busy int
	s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy)
	if fk != 1 || busy != 5000 {
		t.Fatalf("pragmas %d %d", fk, busy)
	}
	for _, table := range []string{"schema_migrations", "users", "backup_codes", "sessions", "jobs", "job_events", "job_claims", "schedules", "events", "secrets", "settings"} {
		var n int
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(table, err)
		}
	}
	s.Close()
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE schema_migrations SET hash='tampered' WHERE version='0001'")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(p); err == nil {
		s.Close()
		t.Fatal("accepted changed migration")
	}
}

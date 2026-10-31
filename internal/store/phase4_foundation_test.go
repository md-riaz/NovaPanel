package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPhase4ProductionMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	// Construct an actual shipped-v5 database, including its recorded hashes.
	// The shipped agent creates its SQLite inode with 0600 before sql.Open.
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	hashesRaw, err := migrations.ReadFile("migrations/hashes.json")
	if err != nil {
		t.Fatal(err)
	}
	var hashes map[string]string
	if err = json.Unmarshal(hashesRaw, &hashes); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) < 5 || name[:4] > "0005" || filepath.Ext(name) != ".sql" {
			continue
		}
		data, e := migrations.ReadFile("migrations/" + name)
		if e != nil {
			t.Fatal(e)
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != hashes[name[:4]] {
			t.Fatal("shipped hash mismatch")
		}
		if _, e = raw.Exec(string(data)); e != nil {
			t.Fatal(e)
		}
		if _, e = raw.Exec("INSERT INTO schema_migrations VALUES(?,?,1)", name[:4], hashes[name[:4]]); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = raw.Exec("INSERT INTO settings VALUES('shipped_v5','true')"); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	if n := phase4Count(t, s.db.raw, "SELECT count(*) FROM schema_migrations"); n != 6 {
		t.Fatalf("want six production migrations, got %d", n)
	}
	candidate, err := os.ReadFile("testdata/phase4_candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	production, err := migrations.ReadFile("migrations/0006_phase4.sql")
	if err != nil {
		t.Fatal(err)
	}
	if string(candidate) != string(production) {
		t.Fatal("production DDL differs from approved candidate")
	}
	phase4Exec(t, s.db.raw, "INSERT INTO settings VALUES('phase4_upgrade','true')")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM settings WHERE key='phase4_upgrade'") != 1 {
		t.Fatal("reopen lost data")
	}
}

func TestTxDeferredCommitFailureRollsBackConnection(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Isolate transaction behavior even before the production migration exists.
	phase4Exec(t, s.db.raw, "CREATE TABLE deferred_parent(id TEXT PRIMARY KEY); CREATE TABLE deferred_child(id TEXT REFERENCES deferred_parent(id) DEFERRABLE INITIALLY DEFERRED)")
	err = s.Tx(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec("INSERT INTO deferred_child VALUES('missing')"); return err })
	if err == nil {
		t.Fatal("expected deferred FK refusal")
	}
	if phase4Count(t, s.db.raw, "SELECT count(*) FROM deferred_child") != 0 {
		t.Fatal("failed transaction leaked on pooled connection")
	}
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO settings VALUES('after_failed_commit','true')")
		return err
	}); err != nil {
		t.Fatalf("pooled connection not reusable: %v", err)
	}
}

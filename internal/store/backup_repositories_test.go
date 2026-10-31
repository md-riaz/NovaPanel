package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/backup"
	"novapanel/internal/secret"
)

// Canonical Phase 6 DDL fixture only. No production migration number is allocated.
const repositoryDDL = `CREATE TABLE repos (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('local','sftp','s3')),
 role TEXT NOT NULL DEFAULT 'site' CHECK(role IN ('site','panel')),
 location TEXT NOT NULL,
 retention_json TEXT NOT NULL DEFAULT '{"keep_daily":7,"keep_weekly":4,"keep_monthly":6}',
 created INTEGER NOT NULL);
 CREATE UNIQUE INDEX repos_one_panel ON repos(role) WHERE role='panel';`

func TestRepositoryMetadataAtomicConflictAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(repositoryDDL); return e }); err != nil {
		t.Fatal(err)
	}
	seal, _ := secret.New(make([]byte, 32))
	password, _ := seal.Seal([]byte("out-of-band-recovery-password"))
	r := backup.Repository{ID: "panel1", Name: "recovery", Kind: "local", Role: "panel", Location: "/srv/recovery"}
	if err = s.CreateRepository(ctx, r, password); err != nil {
		t.Fatal(err)
	}
	r.ID = "panel2"
	r.Name = "other"
	if err = s.CreateRepository(ctx, r, password); !errors.Is(err, ErrRepositoryConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	var n int
	if err = s.db.QueryRow("SELECT count(*) FROM secrets WHERE name='repo:panel2:password'").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.ListRepositories(ctx)
	if err != nil || len(rows) != 1 || rows[0].Role != "panel" || rows[0].Created == 0 {
		t.Fatal(rows, err)
	}
	stored, err := s.RepositoryPassword(ctx, "panel1")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := seal.Open(stored)
	if err != nil || string(plain) != "out-of-band-recovery-password" {
		t.Fatal("password custody failed")
	}
	if _, err = s.RepositoryPassword(ctx, "panel2"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestRepositoryMissingSchemaAndConcurrentPanelPublication(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	sealed := secret.Sealed{Nonce: make([]byte, 12), Ciphertext: make([]byte, 16)}
	r := backup.Repository{ID: "first", Name: "first", Role: "panel", Kind: "local", Location: "/srv/recovery"}
	if err := s.CreateRepository(ctx, r, sealed); !errors.Is(err, ErrRepositoryMetadata) {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(repositoryDDL); return e }); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- s.CreateRepository(ctx, r, sealed) }()
	r2 := r
	r2.ID = "second"
	r2.Name = "second"
	go func() { results <- s.CreateRepository(ctx, r2, sealed) }()
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, ErrRepositoryConflict)) || (b == nil && errors.Is(a, ErrRepositoryConflict))) {
		t.Fatal(a, b)
	}
	rows, err := s.ListRepositories(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	duplicate := rows[0]
	duplicate.ID = "third"
	duplicate.Role = "site"
	if err = s.CreateRepository(ctx, duplicate, sealed); !errors.Is(err, ErrRepositoryConflict) {
		t.Fatal(err)
	}
}

func TestActiveStorePanelSnapshotAndClosedAdmission(t *testing.T) {
	ctx := context.Background()
	s := fresh(t)
	if _, err := s.db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO settings(key,value_json) VALUES('snapshot-marker','true')"); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	if err := os.Chmod(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	snap, err := s.CreatePanelSnapshot(ctx, scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", snap.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v string
	if err = db.QueryRow("SELECT value_json FROM settings WHERE key='snapshot-marker'").Scan(&v); err != nil || v != "true" {
		t.Fatal(v, err)
	}
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&v); err != nil || v != "ok" {
		t.Fatal(v, err)
	}
	if _, err = s.CreatePanelSnapshot(ctx, scratch); !errors.Is(err, ErrStoreClosed) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(scratch)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
}

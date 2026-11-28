package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/exec"
	"novapanel/internal/id"
)

// flakyRevertRunner fails its first failures calls, the way a transient ufw error would.
type flakyRevertRunner struct{ failures, calls int }

func (r *flakyRevertRunner) Run(_ context.Context, _ string, _ []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if r.calls <= r.failures {
		return nil, nil, 1, nil
	}
	return nil, nil, 0, nil
}

// seedRevert creates one unconfirmed pending row with an enabled=0 snapshot and returns its paths.
func seedRevert(t *testing.T) (dbPath, rules, snapshot string, db *sql.DB) {
	t.Helper()
	root := t.TempDir()
	dbPath = filepath.Join(root, "panel.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE fw_pending(id TEXT PRIMARY KEY,snapshot_path TEXT,revert_unit TEXT,deadline INTEGER,confirmed INTEGER); CREATE TABLE jobs(verb TEXT,result_json TEXT,step TEXT)`); err != nil {
		t.Fatal(err)
	}
	snapshot = filepath.Join(root, "snapshot")
	rules = filepath.Join(root, "rules")
	if err = os.MkdirAll(filepath.Join(snapshot, "ufw"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(rules, 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
		if err = os.WriteFile(filepath.Join(snapshot, "ufw", n), []byte(n), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(snapshot, "enabled"), []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pending, _ := id.New()
	if _, err = db.Exec(`INSERT INTO fw_pending VALUES(?,?,?, ?,0)`, pending, snapshot, "timer", 1); err != nil {
		t.Fatal(err)
	}
	return dbPath, rules, snapshot, db
}

func shortRevertRetries(t *testing.T) {
	t.Helper()
	first, max := revertFirstWait, revertMaxWait
	revertFirstWait, revertMaxWait = time.Millisecond, time.Millisecond
	t.Cleanup(func() { revertFirstWait, revertMaxWait = first, max })
}

func TestFirewallRevertRetriesTransientRestore(t *testing.T) {
	shortRevertRetries(t)
	dbPath, rules, snapshot, db := seedRevert(t)
	r := &flakyRevertRunner{failures: 1}
	if err := firewallRevert(context.Background(), dbPath, rules, snapshot, r); err != nil {
		t.Fatal(err)
	}
	if r.calls != 2 {
		t.Fatalf("calls: %d", r.calls)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM fw_pending`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("pending rows %d: %v", count, err)
	}
}

func TestFirewallRevertKeepsPendingWhenRetriesExhausted(t *testing.T) {
	shortRevertRetries(t)
	dbPath, rules, snapshot, db := seedRevert(t)
	r := &flakyRevertRunner{failures: 1 << 30}
	if err := firewallRevert(context.Background(), dbPath, rules, snapshot, r); err == nil {
		t.Fatal("revert reported success after every restore failed")
	}
	if r.calls != revertAttempts {
		t.Fatalf("calls: %d want %d", r.calls, revertAttempts)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM fw_pending`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pending row must survive for a later retry: %d %v", count, err)
	}
}

type revertRunner struct{ calls int }

func (r *revertRunner) Run(_ context.Context, _ string, _ []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	return nil, nil, 0, nil
}
func TestFirewallRevert(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "panel.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE fw_pending(id TEXT PRIMARY KEY,snapshot_path TEXT,revert_unit TEXT,deadline INTEGER,confirmed INTEGER); CREATE TABLE jobs(verb TEXT,result_json TEXT,step TEXT)`); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "snapshot")
	rules := filepath.Join(root, "rules")
	if err = os.MkdirAll(filepath.Join(snapshot, "ufw"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(rules, 0700); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"after.rules", "after6.rules", "before.rules", "before6.rules", "user.rules", "user6.rules"} {
		if err = os.WriteFile(filepath.Join(snapshot, "ufw", n), []byte(n), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(snapshot, "enabled"), []byte("0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pending, _ := id.New()
	if _, err = db.Exec(`INSERT INTO fw_pending VALUES(?,?,?, ?,0)`, pending, snapshot, "timer", 1); err != nil {
		t.Fatal(err)
	}
	r := &revertRunner{}
	if err = firewallRevert(context.Background(), dbPath, rules, snapshot, r); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Fatalf("calls: %d", r.calls)
	}
	if _, err = os.Stat(filepath.Join(root, "fw-reverted-"+pending)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM fw_pending`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count %d: %v", count, err)
	}
	if err = firewallRevert(context.Background(), dbPath, rules, snapshot, r); err != nil || r.calls != 1 {
		t.Fatalf("replay %v calls %d", err, r.calls)
	}
}

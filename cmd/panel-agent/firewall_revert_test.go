package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/exec"
	"novapanel/internal/id"
)

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

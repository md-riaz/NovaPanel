package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
)

type legacyRunner struct {
	calls int
	fail  bool
}

func (r *legacyRunner) Run(ctx context.Context, path string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	if path == "/usr/bin/getent" {
		return []byte("demo:x:123:123::/srv/sites/demo:/usr/sbin/nologin\n"), nil, 0, nil
	}
	if path == "/usr/sbin/usermod" {
		r.calls++
		if r.fail {
			return nil, nil, 1, errors.New("failed")
		}
		return nil, nil, 0, nil
	}
	return nil, nil, 1, errors.New("unexpected command")
}

func TestLegacyMigrationCompletionAndRetry(t *testing.T) {
	ctx := context.Background()
	a := scratchAgent(t)
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.Store = s
	r := &legacyRunner{fail: true}
	a.Exec = r
	a.Jobs = jobs.NewQueue(s, nil, nil)
	a.Cfg.SitesRoot = "/srv/sites"
	const siteID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO sites(id,name,type,docroot,status,created,updated) VALUES(?,?,?,?,'active',0,0)`, siteID, "demo", "php", "/srv/sites/demo/public")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO sites(id,name,type,docroot,status,created,updated) VALUES(?,?,?,?,'creating',0,0)`, "01ARZ3NDEKTSV4RRFFQ69G5FAW", "other", "php", "/srv/sites/other/public")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = migrateLegacyUsers(ctx, a); err == nil {
		t.Fatal("expected failed migration")
	}
	if _, err = s.Setting(ctx, legacyUsersSetting); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("premature completion: %v", err)
	}
	r.fail = false
	if err = migrateLegacyUsers(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = migrateLegacyUsers(ctx, a); err != nil {
		t.Fatal(err)
	}
	if r.calls != 2 {
		t.Fatalf("usermod calls = %d", r.calls)
	}
}

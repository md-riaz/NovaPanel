package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"novapanel/internal/store"
)

func TestLiveReconcileHistoricalOwner(t *testing.T) {
	ctx, runner, name, ident := liveFixture(t)
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO databases(id,name,engine,created) VALUES(?,?,'mariadb',0)`, ident, name)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	lock := func(context.Context, string, string, string) (func(), error) { return func() {}, nil }
	if _, err := Reconcile(ctx, runner, s, lock); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DatabaseWithUsers(ctx, ident); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("orphan owner remains: %v", err)
	}
}

func TestLiveReconcileCreatedAbsentIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, state  string
		wantConflict bool
	}{
		{"failed", "failed", false},
		{"done", "done", true},
		{"unknown", "", true},
		{"live", "queued", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, runner, name, ident := liveFixture(t)
			s, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if tc.state != "" {
				args, _ := json.Marshal(map[string]string{"db_id": ident, "name": name})
				if err := s.CreateJob(ctx, store.Job{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", Verb: "database.create", ObjectKey: "db:" + ident, State: tc.state, Args: args}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := runSQL(ctx, runner, fmt.Sprintf("INSERT INTO novapanel.managed_objects(kind,name,obj_id,phase,created_at) VALUES('schema','%s','%s','created',0);", name, ident)); err != nil {
				t.Fatal(err)
			}
			lock := func(context.Context, string, string, string) (func(), error) { return func() {}, nil }
			changed, err := Reconcile(ctx, runner, s, lock)
			if errors.Is(err, ErrConflict) != tc.wantConflict || (err != nil && !errors.Is(err, ErrConflict)) {
				t.Fatalf("reconcile: %v %v", changed, err)
			}
			want := "1"
			if tc.state == "failed" {
				want = "0"
			}
			state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='schema' AND name='%s';", name))
			if err != nil || state != want {
				t.Fatalf("marker count: %q, want %s: %v", state, want, err)
			}
		})
	}
}

func TestLiveReconcileTerminalAndAmbiguous(t *testing.T) {
	ctx, runner, name, ident := liveFixture(t)
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	args, _ := json.Marshal(map[string]string{"db_id": ident, "name": name})
	if err := s.CreateJob(ctx, store.Job{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", Verb: "database.create", ObjectKey: "db:" + ident, State: "failed", Args: args}); err != nil {
		t.Fatal(err)
	}
	lock := func(context.Context, string, string, string) (func(), error) { return func() {}, nil }
	query := fmt.Sprintf("INSERT INTO novapanel.managed_objects(kind,name,obj_id,phase,created_at) VALUES('schema','%s','%s','created',0); CREATE DATABASE `%s`;", name, ident, name)
	if _, err := runSQL(ctx, runner, query); err != nil {
		t.Fatal(err)
	}
	changed, err := Reconcile(ctx, runner, s, lock)
	if err != nil || len(changed) != 1 {
		t.Fatalf("reconcile: %v %v", changed, err)
	}
	state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s')+(SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='schema' AND name='%s');", name, name))
	if err != nil || state != "0" {
		t.Fatalf("residue: %q %v", state, err)
	}
	// A failed create can leave a created marker even if the object is gone.
	if _, err = runSQL(ctx, runner, fmt.Sprintf("INSERT INTO novapanel.managed_objects(kind,name,obj_id,phase,created_at) VALUES('schema','%s','%s','created',0);", name, ident)); err != nil {
		t.Fatal(err)
	}
	changed, err = Reconcile(ctx, runner, s, lock)
	if err != nil || len(changed) != 1 {
		t.Fatalf("absent created object: %v %v", changed, err)
	}
	state, err = runSQL(ctx, runner, fmt.Sprintf("SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='schema' AND name='%s';", name))
	if err != nil || state != "0" {
		t.Fatalf("absent created marker retained: %q %v", state, err)
	}
	if _, err = runSQL(ctx, runner, fmt.Sprintf("INSERT INTO novapanel.managed_objects(kind,name,obj_id,phase,created_at) VALUES('schema','%s','%s','reserved',0); CREATE DATABASE `%s`;", name, ident, name)); err != nil {
		t.Fatal(err)
	}
	if _, err = Reconcile(ctx, runner, s, lock); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous: %v", err)
	}
	state, err = runSQL(ctx, runner, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s')+(SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='schema' AND name='%s');", name, name))
	if err != nil || state != "2" {
		t.Fatalf("ambiguous state changed: %q %v", state, err)
	}
}

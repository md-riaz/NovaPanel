package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"novapanel/internal/store"
)

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

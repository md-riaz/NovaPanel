package database

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"novapanel/internal/id"
	"novapanel/internal/store"
)

// Exercise each external teardown boundary against the real server. A retry
// must finish removing both the object and its durable ownership marker.
func TestLiveTeardownCrashReplayMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		after        bool
	}{
		{"before_deleting", "UPDATE novapanel.managed_objects SET phase='deleting'", false},
		{"after_deleting", "UPDATE novapanel.managed_objects SET phase='deleting'", true},
		{"before_drop", "DROP DATABASE IF EXISTS `r_", false},
		{"after_drop", "DROP DATABASE IF EXISTS `r_", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, runner, name, ident := liveFixture(t)
			if err := createManaged(ctx, runner, "schema", name, ident, fmt.Sprintf("CREATE DATABASE `%s`", name), nil); err != nil {
				t.Fatal(err)
			}
			s, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			uid, err := id.New()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.ReserveDatabase(ctx, store.Database{ID: ident, Name: name, Engine: "mariadb"}, store.DatabaseUser{ID: uid, DatabaseID: ident, Username: name}); err != nil {
				t.Fatal(err)
			}
			jid, err := id.New()
			if err != nil {
				t.Fatal(err)
			}
			args, err := json.Marshal(map[string]string{"db_id": ident, "name": name})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CreateJob(ctx, store.Job{ID: jid, Verb: "database.create", ObjectKey: "db:" + ident, State: "done", Args: args}); err != nil {
				t.Fatal(err)
			}
			jid, err = id.New()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CreateJob(ctx, store.Job{ID: jid, Verb: "database.delete", ObjectKey: "db:" + ident, State: "running", Args: args}); err != nil {
				t.Fatal(err)
			}
			stop := &stopSQL{delegate: runner, prefix: tc.prefix, after: tc.after}
			if err := ManagedDelete(ctx, stop, name, ident, nil); err == nil || !stop.hit {
				t.Fatalf("stop not reached: %v hit=%v", err, stop.hit)
			}
			lock := func(context.Context, string, string, string) (func(), error) { return func() {}, nil }
			if _, err := Reconcile(ctx, runner, s, lock); err != nil {
				t.Fatalf("reconcile interrupted delete: %v", err)
			}
			for i := 0; i < 2; i++ {
				if err := ManagedDelete(ctx, runner, name, ident, nil); err != nil {
					t.Fatalf("replay %d: %v", i, err)
				}
			}
			j, err := s.GetJob(ctx, jid)
			if err != nil {
				t.Fatal(err)
			}
			j.State = "done"
			if err = s.UpdateJob(ctx, j); err != nil {
				t.Fatal(err)
			}
			if _, err = Reconcile(ctx, runner, s, lock); err != nil {
				t.Fatal(err)
			}
			state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),(SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='schema' AND name='%s');", name, name))
			if err != nil || state != "0\t0" {
				t.Fatalf("residue %q: %v", state, err)
			}
		})
	}
}

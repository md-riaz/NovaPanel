package database

import (
	"fmt"
	"testing"
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
		{"before_marker_delete", "DELETE FROM novapanel.managed_objects", false},
		{"after_marker_delete", "DELETE FROM novapanel.managed_objects", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, runner, name, ident := liveFixture(t)
			if err := createManaged(ctx, runner, "schema", name, ident, fmt.Sprintf("CREATE DATABASE `%s`", name), nil); err != nil {
				t.Fatal(err)
			}
			stop := &stopSQL{delegate: runner, prefix: tc.prefix, after: tc.after}
			if err := ManagedDelete(ctx, stop, name, ident, nil); err == nil || !stop.hit {
				t.Fatalf("stop not reached: %v hit=%v", err, stop.hit)
			}
			for i := 0; i < 2; i++ {
				if err := ManagedDelete(ctx, runner, name, ident, nil); err != nil {
					t.Fatalf("replay %d: %v", i, err)
				}
			}
			state, err := runSQL(ctx, runner, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),(SELECT COUNT(*) FROM novapanel.managed_objects WHERE kind='schema' AND name='%s');", name, name))
			if err != nil || state != "0\t0" {
				t.Fatalf("residue %q: %v", state, err)
			}
		})
	}
}

package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/store"
)

// stopSQL simulates losing the process immediately before or after a single SQL statement.
type stopSQL struct {
	delegate   exec.Runner
	prefix     string
	after, hit bool
}

func (s *stopSQL) Run(ctx context.Context, program string, args []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(opts.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	opts.Stdin = strings.NewReader(string(b))
	match := !s.hit && strings.HasPrefix(string(b), s.prefix)
	if match {
		s.hit = true
		if !s.after {
			return nil, nil, -1, errors.New("simulated crash")
		}
	}
	out, stderr, code, err := s.delegate.Run(ctx, program, args, opts)
	if match && err == nil && code == 0 {
		return nil, nil, -1, errors.New("simulated crash")
	}
	return out, stderr, code, err
}

func TestLiveCrashSQLReplayMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, prefix    string
		after, conflict bool
	}{
		{"before_reserve", "INSERT INTO novapanel.managed_objects", false, false},
		{"after_reserve", "INSERT INTO novapanel.managed_objects", true, false},
		{"before_create", "CREATE DATABASE `r_", false, false},
		{"after_create_ambiguous", "CREATE DATABASE `r_", true, true},
		{"before_created", "UPDATE novapanel.managed_objects SET phase='created'", false, true},
		{"after_created", "UPDATE novapanel.managed_objects SET phase='created'", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, r, name, ident := liveFixture(t)
			s, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			jid, err := id.New()
			if err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]string{"db_id": ident, "name": name})
			if err = s.CreateJob(ctx, store.Job{ID: jid, Verb: "database.create", ObjectKey: "db:" + ident, State: "running", Args: args}); err != nil {
				t.Fatal(err)
			}
			stop := &stopSQL{delegate: r, prefix: tc.prefix, after: tc.after}
			create := fmt.Sprintf("CREATE DATABASE `%s`", name)
			if err = createManaged(ctx, stop, "schema", name, ident, create, nil); err == nil || !stop.hit {
				t.Fatalf("stop not reached: %v hit=%v", err, stop.hit)
			}
			lock := func(context.Context, string, string, string) (func(), error) { return func() {}, nil }
			if _, err = Reconcile(ctx, r, s, lock); err != nil {
				t.Fatalf("reconcile live job: %v", err)
			}
			err = createManaged(ctx, r, "schema", name, ident, create, nil)
			if tc.conflict {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("expected conflict: %v", err)
				}
			} else if err != nil {
				t.Fatalf("replay: %v", err)
			}
			phase := "created"
			if tc.conflict {
				phase = "reserved"
			}
			state, err := runSQL(ctx, r, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),(SELECT phase FROM novapanel.managed_objects WHERE kind='schema' AND name='%s' AND obj_id='%s');", name, name, ident))
			if err != nil || state != "1\t"+phase {
				t.Fatalf("state %q: %v", state, err)
			}
		})
	}
}

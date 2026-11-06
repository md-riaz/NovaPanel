package database

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"novapanel/internal/exec"
	"novapanel/internal/store"
)

type ownerRunner struct {
	state      string
	statements []string
}

func (r *ownerRunner) Run(_ context.Context, _ string, _ []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, _ := io.ReadAll(o.Stdin)
	r.statements = append(r.statements, string(b))
	return []byte(r.state + "\n"), nil, 0, nil
}

type scanRunner struct{}

func (scanRunner) Run(_ context.Context, _ string, _ []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, _ := io.ReadAll(o.Stdin)
	q := string(b)
	switch {
	case strings.Contains(q, "SCHEMA_NAME='novapanel'"):
		return []byte("1\n"), nil, 0, nil
	case strings.Contains(q, "information_schema.COLUMNS"):
		return []byte("1:5:5:2:2:0:0:0\n"), nil, 0, nil
	case strings.Contains(q, "information_schema.USER_PRIVILEGES"):
		return []byte("0\n"), nil, 0, nil
	case strings.Contains(q, "ORDER BY kind,name"):
		return nil, nil, 0, nil
	default:
		return []byte("0\t0\t0\n"), nil, 0, nil
	}
}

func TestReconcileOwnerScanSkipsDisappearedDatabase(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ids := []string{"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW", "01ARZ3NDEKTSV4RRFFQ69G5FAX"}
	names := []string{"firstdb", "seconddb", "thirddb"}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		for i := range ids {
			if _, err := tx.Exec(`INSERT INTO databases(id,name,engine,created) VALUES(?,?,'mariadb',0)`, ids[i], names[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lock := func(_ context.Context, dbID, _, _ string) (func(), error) {
		if dbID == ids[0] {
			if err := s.DeleteDatabase(ctx, ids[1], names[1]); err != nil {
				return nil, err
			}
		}
		return func() {}, nil
	}
	changed, err := Reconcile(ctx, scanRunner{}, s, lock)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(changed) != 2 || changed[0] != "schema:firstdb" || changed[1] != "schema:thirddb" {
		t.Fatalf("remaining owners not reconciled: %v", changed)
	}
}

func TestReconcileOwnerSafety(t *testing.T) {
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	for _, tc := range []struct {
		kind, state  string
		live, remove bool
	}{
		{"user", "1\t1\t0", false, true}, {"user", "0\t0", false, true}, {"user", "0\t1", false, false}, {"user", "1\t0", false, false}, {"user", "0\t0", true, false},
		{"schema", "1\t1\t0", false, false}, {"schema", "0\t1", false, false}, {"schema", "1\t0", false, false},
	} {
		t.Run(tc.kind+strings.ReplaceAll(tc.state, "\t", "_")+map[bool]string{true: "live", false: ""}[tc.live], func(t *testing.T) {
			ctx := context.Background()
			s, e := store.Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			e = s.Tx(ctx, func(tx *sql.Tx) error {
				if _, e := tx.Exec(`INSERT INTO databases(id,name,engine,created) VALUES(?,'testdb','mariadb',0)`, db); e != nil {
					return e
				}
				_, e := tx.Exec(`INSERT INTO db_users(id,db_id,username,host,owner,created) VALUES(?,?,'testuser','127.0.0.1',0,0)`, user, db)
				return e
			})
			if e != nil {
				t.Fatal(e)
			}
			if tc.live {
				e = s.CreateJob(ctx, store.Job{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", Verb: "dbuser.delete", ObjectKey: "db:" + db, State: "queued", Args: []byte(`{}`)})
				if e != nil {
					t.Fatal(e)
				}
			}
			state := tc.state
			if strings.Count(state, "\t") == 1 {
				if strings.HasPrefix(state, "1") {
					state += "\t1"
				} else {
					state += "\t0"
				}
			}
			r := &ownerRunner{state: state}
			lock := func(context.Context, string, string, string) (func(), error) { return func() {}, nil }
			name, ident := "testuser", user
			if tc.kind == "schema" {
				name, ident = "testdb", db
			}
			removed, e := reconcileOwner(ctx, r, s, lock, tc.kind, name, ident, db)
			if (tc.state == "0\t1" || tc.state == "1\t1\t0") && !tc.live {
				if !errors.Is(e, ErrConflict) {
					t.Fatalf("expected conflict: %v", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if removed != tc.remove {
				t.Fatalf("removed=%v", removed)
			}
			for _, q := range r.statements {
				if strings.Contains(q, "DROP ") {
					t.Fatalf("unsafe SQL %s", q)
				}
			}
			if tc.kind == "user" {
				_, e = s.DatabaseUserByID(ctx, user)
				if errors.Is(e, sql.ErrNoRows) != tc.remove {
					t.Fatalf("owner: %v", e)
				}
			}
		})
	}
}

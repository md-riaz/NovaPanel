package database

import (
	"context"
	"io"
	"novapanel/internal/exec"
	"strings"
	"testing"
)

func TestAbortMarksAllBeforeDroppingAndRetainsMarkers(t *testing.T) {
	r := &provenanceRunner{state: "created\t1\t01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	err := AbortCreate(context.Background(), r, []AbortObject{{"schema", "d_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, {"user", "u_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV"}})
	if err != nil {
		t.Fatal(err)
	}
	marks, firstDrop := 0, -1
	for i, s := range r.statements {
		if strings.Contains(s, "SET phase='deleting'") {
			marks++
		}
		if strings.HasPrefix(s, "DROP ") && firstDrop < 0 {
			firstDrop = i
			if marks != 2 {
				t.Fatal("drop preceded marking all owned objects")
			}
		}
		if strings.HasPrefix(s, "DELETE ") {
			t.Fatal("removed marker before terminal job")
		}
	}
	if marks != 2 || firstDrop < 0 {
		t.Fatalf("missing abort actions: %v", r.statements)
	}
}

type rollbackRunner struct {
	schema, user string
	statements   []string
}

func (r *rollbackRunner) Run(_ context.Context, _ string, _ []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	raw, _ := io.ReadAll(o.Stdin)
	sql := string(raw)
	r.statements = append(r.statements, sql)
	if strings.Contains(sql, "SELECT COALESCE") && strings.Contains(sql, "information_schema.SCHEMATA") {
		return []byte(r.schema + "\n"), nil, 0, nil
	}
	if strings.Contains(sql, "SELECT COALESCE") && strings.Contains(sql, "mysql.user") {
		return []byte(r.user + "\n"), nil, 0, nil
	}
	if strings.Contains(sql, "SELECT COALESCE") {
		return []byte("created\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}
func TestRollbackFreshCreateChecksAllOwnershipBeforeEffects(t *testing.T) {
	const owner = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	for _, tc := range []struct {
		name, schema, user string
		wantError          bool
	}{
		{"created", "created\t1\t" + owner, "created\t1\t" + owner, false},
		{"reserved absent", "reserved\t0\t" + owner, "reserved\t0\t" + owner, false},
		{"reserved present", "reserved\t1\t" + owner, "created\t1\t" + owner, true},
		{"foreign", "created\t1\t01ARZ3NDEKTSV4RRFFQ69G5FAW", "created\t1\t" + owner, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &rollbackRunner{schema: tc.schema, user: tc.user}
			err := RollbackFreshCreate(context.Background(), r, "d_test", owner, "u_test", owner)
			if (err != nil) != tc.wantError {
				t.Fatalf("error %v queries %v", err, r.statements)
			}
			if tc.wantError {
				for _, s := range r.statements {
					if strings.HasPrefix(s, "DROP ") || strings.HasPrefix(s, "DELETE ") || strings.HasPrefix(s, "UPDATE ") {
						t.Fatalf("effect before ownership checks: %s", s)
					}
				}
			}
		})
	}
}

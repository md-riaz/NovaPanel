package database

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"novapanel/internal/exec"
)

type deleteReplayRunner struct {
	present    string
	phase      string
	statements []string
}

func (r *deleteReplayRunner) Run(_ context.Context, _ string, _ []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, _ := io.ReadAll(opts.Stdin)
	s := string(b)
	r.statements = append(r.statements, s)
	if strings.Contains(s, "SELECT COALESCE") {
		if r.phase == "foreign" {
			if strings.Contains(s, "AND obj_id=") {
				return []byte("none\n"), nil, 0, nil
			}
			return []byte("foreign\n"), nil, 0, nil
		}
		if r.phase != "" {
			return []byte(r.phase + "\n"), nil, 0, nil
		}
		return []byte("none\n"), nil, 0, nil
	}
	if strings.Contains(s, "SELECT COUNT") {
		return []byte(r.present + "\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}
func TestDeleteRejectsForeignMarker(t *testing.T) {
	r := &deleteReplayRunner{phase: "foreign"}
	if err := ManagedDeleteUser(context.Background(), r, "u_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign marker: %v", err)
	}
	for _, statement := range r.statements {
		if strings.HasPrefix(statement, "DROP ") {
			t.Fatal("dropped foreign object")
		}
	}
}

func TestDeleteRemovesMarkerAfterDrop(t *testing.T) {
	r := &deleteReplayRunner{phase: "created"}
	if err := ManagedDeleteUser(context.Background(), r, "u_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV"); err != nil {
		t.Fatal(err)
	}
	if len(r.statements) != 4 || !strings.HasPrefix(r.statements[2], "DROP USER") || !strings.HasPrefix(r.statements[3], "DELETE FROM novapanel.managed_objects") {
		t.Fatalf("incorrect teardown order: %v", r.statements)
	}
}

func TestDeleteMatchesResourceIdentity(t *testing.T) {
	r := &deleteReplayRunner{}
	if err := ManagedDeleteUser(context.Background(), r, "u_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.statements[0], "obj_id='01ARZ3NDEKTSV4RRFFQ69G5FAV'") {
		t.Fatal("delete did not test ownership")
	}
}

func TestDeleteReplayMissingMarker(t *testing.T) {
	for _, tc := range []struct {
		present  string
		conflict bool
	}{{"0", false}, {"1", false}} {
		r := &deleteReplayRunner{present: tc.present}
		err := ManagedDeleteUser(context.Background(), r, "u_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV")
		if (err != nil) != tc.conflict {
			t.Fatalf("present=%s: %v", tc.present, err)
		}
		for _, s := range r.statements {
			if strings.Contains(s, "DROP USER") {
				t.Fatal("unproven user was dropped")
			}
		}
	}
}

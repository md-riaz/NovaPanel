package database

import (
	"context"
	"errors"
	"io"
	"novapanel/internal/exec"
	"strings"
	"testing"
)

type provenanceRunner struct {
	state      string
	statements []string
}

func (r *provenanceRunner) Run(_ context.Context, _ string, _ []string, opts exec.RunOpts) ([]byte, []byte, int, error) {
	b, _ := io.ReadAll(opts.Stdin)
	s := string(b)
	r.statements = append(r.statements, s)
	if strings.Contains(s, "SELECT COALESCE") {
		return []byte(r.state + "\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}
func TestBootstrapRejectsUnverifiedExistingSchema(t *testing.T) {
	r := &provenanceRunner{state: "1"}
	if err := Bootstrap(context.Background(), r); err == nil {
		t.Fatal("accepted unverified existing schema")
	}
	for _, s := range r.statements {
		if strings.Contains(s, "CREATE ") {
			t.Fatalf("modified existing schema: %s", s)
		}
	}
}

func TestProvenanceUsesResourceIDColumn(t *testing.T) {
	r := &provenanceRunner{state: "none\t0\t"}
	err := createManaged(context.Background(), r, "schema", "d_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "CREATE DATABASE `d_test`", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range r.statements {
		if strings.Contains(s, "CREATE DATABASE IF NOT EXISTS `novapanel`") {
			t.Fatal("create must not bootstrap control schema")
		}
		if strings.Contains(s, "op_id") {
			t.Fatalf("provenance uses job identity: %s", s)
		}
	}
}

func TestProvenanceCommitsAfterFinish(t *testing.T) {
	r := &provenanceRunner{state: "none\t0\t"}
	err := createManaged(context.Background(), r, "schema", "d_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "CREATE DATABASE `d_test`", func() error {
		for _, s := range r.statements {
			if strings.Contains(s, "SET phase='created'") {
				t.Fatal("committed before finish")
			}
		}
		return errors.New("grant failed")
	})
	if err == nil {
		t.Fatal("expected finish error")
	}
	for _, s := range r.statements {
		if strings.Contains(s, "SET phase='created'") {
			t.Fatal("committed after failed finish")
		}
	}
}

func TestProvenanceCreatedReplaysFinish(t *testing.T) {
	r := &provenanceRunner{state: "created\t1\t01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	called := false
	err := createManaged(context.Background(), r, "user", "u_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "CREATE USER", func() error { called = true; return nil })
	if err != nil || !called {
		t.Fatalf("finish not replayed: called=%v err=%v", called, err)
	}
	for _, s := range r.statements {
		if strings.Contains(s, "CREATE USER") {
			t.Fatal("recreated owned user")
		}
	}
}

func TestProvenanceConflictMatrix(t *testing.T) {
	for _, state := range []string{"none\t1\t", "created\t0\t01ARZ3NDEKTSV4RRFFQ69G5FAV", "created\t1\t01ARZ3NDEKTSV4RRFFQ69G5FAW", "reserved\t0\t01ARZ3NDEKTSV4RRFFQ69G5FAW"} {
		r := &provenanceRunner{state: state}
		err := createManaged(context.Background(), r, "schema", "d_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "CREATE DATABASE `d_test`", nil)
		if !errors.Is(err, ErrConflict) {
			t.Errorf("%q: expected conflict, got %v", state, err)
		}
		for _, s := range r.statements {
			if strings.Contains(s, "CREATE DATABASE `d_test`") || strings.HasPrefix(s, "DROP ") {
				t.Fatalf("%q changed unowned object: %s", state, s)
			}
		}
	}
}

func TestProvenanceRefusesReservedPresent(t *testing.T) {
	r := &provenanceRunner{state: "reserved\t1\t01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	err := createManaged(context.Background(), r, "schema", "d_test", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "CREATE DATABASE `d_test`", nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	for _, s := range r.statements {
		if strings.Contains(s, "CREATE DATABASE `d_test`") {
			t.Fatal("adopted unproven object")
		}
	}
}

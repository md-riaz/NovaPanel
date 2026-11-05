package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"novapanel/internal/exec"
	"novapanel/internal/id"
)

// These destructive SQL tests must run only inside a disposable, network-isolated
// MariaDB container. The gate is deliberately separate from the older live tests.
func liveFixture(t *testing.T) (context.Context, exec.Runner, string, string) {
	t.Helper()
	if os.Getenv("NOVAPANEL_MARIADB_REPLAY_TEST") != "1" {
		t.Skip("requires disposable MariaDB replay fixture")
	}
	if os.Getenv("NOVAPANEL_MARIADB_DISPOSABLE") != "network-none" {
		t.Fatal("explicit disposable network-none fixture assertion required")
	}
	marker, err := os.ReadFile("/run/novapanel-replay-fixture")
	if err != nil || strings.TrimSpace(string(marker)) != "disposable-network-none" {
		t.Fatal("disposable fixture marker missing")
	}
	ident, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	name := "r_" + strings.ToLower(ident[6:])
	ctx := context.Background()
	r := exec.Real{}
	if err := Bootstrap(ctx, r); err != nil {
		t.Fatalf("fixture bootstrap: %v", err)
	}
	t.Cleanup(func() {
		_, err := runSQL(ctx, r, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`; DELETE FROM novapanel.managed_objects WHERE kind='schema' AND name='%s' AND obj_id='%s';", name, name, ident))
		if err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
	})
	return ctx, r, name, ident
}

type stopBeforeMarkerDelete struct {
	delegate exec.Runner
	stopped  bool
}

func (r *stopBeforeMarkerDelete) Run(ctx context.Context, program string, args []string, o exec.RunOpts) ([]byte, []byte, int, error) {
	b, err := io.ReadAll(o.Stdin)
	if err != nil {
		return nil, nil, -1, err
	}
	o.Stdin = strings.NewReader(string(b))
	if strings.HasPrefix(string(b), "DELETE FROM novapanel.managed_objects") {
		r.stopped = true
		return nil, nil, 1, nil
	}
	return r.delegate.Run(ctx, program, args, o)
}

func TestLiveManagedRotateOwnership(t *testing.T) {
	ctx, r, user, ident := liveFixture(t)
	other, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runSQL(ctx, r, fmt.Sprintf("CREATE USER '%s'@'127.0.0.1' IDENTIFIED BY 'Original123';", user)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := runSQL(ctx, r, fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1'; DELETE FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1';", user, user)); err != nil {
			t.Errorf("user cleanup: %v", err)
		}
	})
	for _, owner := range []string{"", other, ident} {
		if owner != "" {
			if _, err = runSQL(ctx, r, fmt.Sprintf("INSERT INTO novapanel.managed_objects(kind,name,obj_id,phase,created_at) VALUES('user','%s@127.0.0.1','%s','created',0);", user, owner)); err != nil {
				t.Fatal(err)
			}
		}
		err = ManagedRotate(ctx, r, user, ident, "Changed123")
		if owner != ident && !errors.Is(err, ErrConflict) || owner == ident && err != nil {
			t.Fatalf("owner %q: %v", owner, err)
		}
		out, err := runSQL(ctx, r, fmt.Sprintf("SELECT authentication_string=PASSWORD('%s') FROM mysql.user WHERE User='%s' AND Host='127.0.0.1';", map[bool]string{true: "Changed123", false: "Original123"}[owner == ident], user))
		if err != nil || out != "1" {
			t.Fatalf("password changed unexpectedly: %q %v", out, err)
		}
		if owner != "" {
			if _, err = runSQL(ctx, r, fmt.Sprintf("DELETE FROM novapanel.managed_objects WHERE kind='user' AND name='%s@127.0.0.1';", user)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestLiveManagedDeleteReplay(t *testing.T) {
	ctx, r, name, ident := liveFixture(t)
	create := fmt.Sprintf("CREATE DATABASE `%s`", name)
	if err := createManaged(ctx, r, "schema", name, ident, create, nil); err != nil {
		t.Fatal(err)
	}
	interrupted := &stopBeforeMarkerDelete{delegate: r}
	if err := ManagedDelete(ctx, interrupted, name, ident, nil); err == nil || !interrupted.stopped {
		t.Fatalf("expected interruption before marker removal: %v", err)
	}
	query := fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),COALESCE((SELECT phase FROM novapanel.managed_objects WHERE kind='schema' AND name='%s' AND obj_id='%s'),'none');", name, name, ident)
	state, err := runSQL(ctx, r, query)
	if err != nil || state != "0\tdeleting" {
		t.Fatalf("mid-delete state %q: %v", state, err)
	}
	if err := ManagedDelete(ctx, r, name, ident, nil); err != nil {
		t.Fatalf("replay: %v", err)
	}
	state, err = runSQL(ctx, r, query)
	if err != nil || state != "0\tnone" {
		t.Fatalf("replayed state %q: %v", state, err)
	}
	if err := ManagedDelete(ctx, r, name, ident, nil); err != nil {
		t.Fatalf("second replay: %v", err)
	}
}

func TestLiveManagedCreateReplayMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, phase       string
		present, conflict bool
	}{
		{"reserved_absent", "reserved", false, false},
		{"reserved_present", "reserved", true, true},
		{"created_missing", "created", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, r, name, ident := liveFixture(t)
			_, err := runSQL(ctx, r, fmt.Sprintf("INSERT INTO novapanel.managed_objects(kind,name,obj_id,phase,created_at) VALUES('schema','%s','%s','%s',0);", name, ident, tc.phase))
			if err != nil {
				t.Fatal(err)
			}
			if tc.present {
				if _, err = runSQL(ctx, r, fmt.Sprintf("CREATE DATABASE `%s`;", name)); err != nil {
					t.Fatal(err)
				}
			}
			err = createManaged(ctx, r, "schema", name, ident, fmt.Sprintf("CREATE DATABASE `%s`", name), nil)
			if tc.conflict {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("want conflict: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			expectedPhase, expectedPresent := tc.phase, tc.present
			if !tc.conflict {
				expectedPhase, expectedPresent = "created", true
			}
			want := "0"
			if expectedPresent {
				want = "1"
			}
			state, err := runSQL(ctx, r, fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s'),(SELECT phase FROM novapanel.managed_objects WHERE kind='schema' AND name='%s' AND obj_id='%s');", name, name, ident))
			if err != nil || state != want+"\t"+expectedPhase {
				t.Fatalf("state %q: %v", state, err)
			}
		})
	}
}

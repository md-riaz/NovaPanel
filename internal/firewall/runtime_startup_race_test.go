package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeStartupReinspection(t *testing.T) {
	for _, state := range []string{"confirmed", "reverted", "acknowledged", "confirming"} {
		t.Run(state, func(t *testing.T) {
			l, r, _ := runtimeFixture(t, true)
			f := newManagerFixture(t, r)
			changed := false
			runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
				if p == "/usr/bin/systemd-run" && !changed {
					changed = true
					j, e := OpenJournal(l.root + "/journal")
					mustCustody(t, e)
					r.State = state
					mustCustody(t, j.publish(r))
					mustCustody(t, j.Close())
				}
				return nil, nil, 0, nil, false
			}}
			m := runtimeManager{runner: runner, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
			e := startupFirewall(context.Background(), l, m)
			if !errors.Is(e, ErrFirewallRecoveryPending) {
				t.Fatal(e)
			}
			j, e := OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			got, _, e := j.Inspect(context.Background())
			mustCustody(t, e)
			mustCustody(t, j.Close())
			if got.State != state {
				t.Fatal("startup changed raced record", got)
			}
		})
	}
}
func TestJournalInspectUnsafeAbsence(t *testing.T) {
	for _, attack := range []string{"malformed", "symlink", "missing-root", "replaced-lock", "state-next"} {
		t.Run(attack, func(t *testing.T) {
			j, _ := journalFixture(t)
			switch attack {
			case "malformed":
				mustCustody(t, os.WriteFile(j.root+"/state", []byte(`{}`), 0600))
			case "symlink":
				mustCustody(t, os.Symlink("absent", j.root+"/state"))
			case "missing-root":
				mustCustody(t, os.Rename(j.root, j.root+".gone"))
				t.Cleanup(func() { os.Rename(j.root+".gone", j.root) })
			case "replaced-lock":
				mustCustody(t, os.Rename(j.root+"/lock", j.root+"/old-lock"))
				mustCustody(t, os.WriteFile(j.root+"/lock", nil, 0600))
			case "state-next":
				mustCustody(t, os.WriteFile(j.root+"/state.next", []byte("retained"), 0600))
			}
			_, present, e := j.Inspect(context.Background())
			if attack == "state-next" {
				if e != nil || present {
					t.Fatal(e, present)
				}
				b, e := os.ReadFile(filepath.Join(j.root, "state.next"))
				if e != nil || string(b) != "retained" {
					t.Fatal("inspection consumed state.next", e)
				}
				return
			}
			if e == nil {
				t.Fatal("unsafe absence admitted")
			}
		})
	}
}
func TestRuntimeManagerConfirmFailureRetainsCommit(t *testing.T) {
	for _, fault := range []string{"prepare", "deadline", "stop", "readback"} {
		t.Run(fault, func(t *testing.T) {
			j, r := journalFixture(t)
			mustCustody(t, j.Arm(context.Background(), r))
			f := newManagerFixture(t, r)
			calls := 0
			runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
				calls++
				if fault == "stop" || fault == "readback" && p == "/usr/bin/busctl" {
					return nil, nil, 1, errors.New("manager failure"), true
				}
				return nil, nil, 0, nil, true
			}}
			m := runtimeManager{runner: runner}
			now := int64(90)
			if fault == "deadline" {
				now = 100
			}
			if fault == "prepare" {
				j.syncDirectory = func() error { return errors.New("fsync failure") }
			}
			e := j.Confirm(context.Background(), r.ID, func() time.Time { return time.Unix(now, 0) }, func(stem string) error { return m.stop(context.Background(), stem) })
			if e == nil {
				t.Fatal("fault succeeded")
			}
			if fault == "prepare" || fault == "deadline" {
				if calls != 0 {
					t.Fatal("stopped before commit")
				}
			} else if !errors.Is(e, ErrConfirmationCommitted) {
				t.Fatal(e)
			}
			got, e := j.read()
			mustCustody(t, e)
			if got.ID != r.ID {
				t.Fatal("lost evidence")
			}
		})
	}
}

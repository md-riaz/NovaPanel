package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeStartupMatrix(t *testing.T) {
	for _, state := range []string{"absent", "armed", "confirming", "reverting", "confirmed", "reverted", "acknowledged"} {
		t.Run(state, func(t *testing.T) {
			l, r, _ := runtimeFixture(t, true)
			j, e := OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			if state == "absent" {
				mustCustody(t, os.Remove(filepath.Join(l.root, "journal/state")))
			} else {
				r.State = state
				mustCustody(t, j.publish(r))
			}
			mustCustody(t, j.Close())
			before, _ := os.ReadFile(l.root + "/journal/state")
			f := newManagerFixture(t, r)
			m := runtimeManager{runner: f, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
			e = startupFirewall(context.Background(), l, m)
			if state == "absent" {
				if e != nil || len(f.calls) != 0 {
					t.Fatal(e, f.calls)
				}
			} else {
				if !errors.Is(e, ErrFirewallRecoveryPending) || errors.Is(e, errManager) {
					t.Fatal(e)
				}
				if (state == "armed" || state == "confirming") && len(f.calls) == 0 {
					t.Fatal("no actual manager execution")
				}
			}
			after, _ := os.ReadFile(l.root + "/journal/state")
			if string(before) != string(after) {
				t.Fatal("startup mutated authority")
			}
		})
	}
}

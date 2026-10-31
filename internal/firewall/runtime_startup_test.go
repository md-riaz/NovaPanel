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

func TestRuntimeStartupPlanPendingAdmission(t *testing.T) {
	for _, state := range []string{"armed", "confirming", "reverting"} {
		for _, now := range []int64{90, 100, 101} {
			t.Run(state+time.Unix(now, 0).Format("05"), func(t *testing.T) {
				l, r, _ := runtimeFixture(t, true)
				j, e := OpenJournal(l.root + "/journal")
				mustCustody(t, e)
				defer j.Close()
				r.State = state
				mustCustody(t, j.publish(r))
				f := newManagerFixture(t, r)
				m := runtimeManager{runner: f, now: func() time.Time { return time.Unix(now, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
				p, e := prepareFirewallStartup(t.Context(), l, m)
				if p != nil || !errors.Is(e, ErrFirewallRecoveryPending) || len(f.calls) == 0 {
					t.Fatal("pending admitted or manager bypassed", p, e, f.calls)
				}
			})
		}
	}
}

func TestRuntimeStartupPlanCanceledRecheck(t *testing.T) {
	l, r, _ := runtimeFixture(t, true)
	j, e := OpenJournal(l.root + "/journal")
	mustCustody(t, e)
	defer j.Close()
	r.State = "confirmed"
	mustCustody(t, j.publish(r))
	p, e := prepareFirewallStartup(t.Context(), l, runtimeManager{})
	mustCustody(t, e)
	defer p.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- j.locked(t.Context(), func() error { close(entered); <-release; return nil }) }()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	e = p.Reconcile(ctx, func(JournalRecord) error { t.Error("canceled startup committed"); return nil }, nil, nil)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	close(release)
	mustCustody(t, <-done)
	raw, e := os.ReadFile(l.root + "/journal/state")
	mustCustody(t, e)
	if string(raw) == "" {
		t.Fatal("lost evidence")
	}
}

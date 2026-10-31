package firewall

import (
	"errors"
	"testing"
)

func TestCommitTerminalRetainsDurableEvidence(t *testing.T) {
	for _, state := range []string{"confirmed", "reverted", "armed", "confirming", "reverting", "acknowledged"} {
		t.Run(state, func(t *testing.T) {
			l, r, _ := runtimeFixture(t, true)
			j, e := OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			defer j.Close()
			r.State = state
			mustCustody(t, j.publish(r))
			calls := 0
			commit := func(got JournalRecord) error {
				calls++
				if got != r {
					t.Fatal(got)
				}
				return nil
			}
			run := func() error { return j.CommitTerminal(t.Context(), commit) }
			terminal := state == "confirmed" || state == "reverted"
			e = run()
			if (e == nil) != terminal {
				t.Fatalf("admission %s: %v", state, e)
			}
			got, e := j.read()
			mustCustody(t, e)
			if got != r || calls != map[bool]int{true: 1, false: 0}[terminal] {
				t.Fatalf("evidence/callback changed: %+v calls %d", got, calls)
			}
			if terminal {
				j.syncDirectory = func() error { return errors.New("sync fault") }
				e = run()
				if e == nil || calls != 1 {
					t.Fatal("sync failure reached commit", e, calls)
				}
				if state == "confirmed" && !errors.Is(e, ErrConfirmationCommitted) {
					t.Fatal(e)
				}
			}
		})
	}
}

package firewall

import (
	"context"
	"os"
	"testing"
	"time"
)

// Regression assertions retained from independent review, with real scratch locks.
func TestRuntimeRegressionTerminalWithoutRestore(t *testing.T) {
	for _, state := range []string{"confirmed", "reverted", "acknowledged"} {
		t.Run(state, func(t *testing.T) {
			l, r, f := runtimeFixture(t, true)
			j, e := OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			r.State = state
			mustCustody(t, j.locked(t.Context(), func() error { return j.publish(r) }))
			mustCustody(t, j.Close())
			mustCustody(t, os.Rename(l.rules, l.rules+".offline"))
			mustCustody(t, os.Rename(l.root+"/restore", l.root+"/restore.offline"))
			e = recoverInstalled(t.Context(), r.ID, l, func() time.Time { return time.Unix(r.Deadline, 0) }, f)
			if e != nil {
				t.Errorf("terminal %s depends on unavailable restore destination: %v", state, e)
			}
			if len(f.calls) != 0 {
				t.Fatal("terminal invoked command")
			}
		})
	}
}

func TestRuntimeRegressionCanceledJournalOpen(t *testing.T) {
	l, r, f := runtimeFixture(t, true)
	j, e := OpenJournal(l.root + "/journal")
	mustCustody(t, e)
	defer j.Close()
	entered, release, holder := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		holder <- j.locked(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		done <- recoverInstalled(ctx, r.ID, l, func() time.Time { return time.Unix(r.Deadline, 0) }, f)
	}()
	timedOut := false
	select {
	case <-done:
	case <-time.After(150 * time.Millisecond):
		timedOut = true
	}
	close(release)
	mustCustody(t, <-holder)
	if timedOut {
		<-done
		t.Error("already-canceled recovery waited for journal lock release inside OpenJournal")
	}
	if len(f.calls) != 0 {
		t.Fatal("canceled recovery invoked command")
	}
}

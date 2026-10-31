package firewall

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestCommitTerminalFlockSerializesFireAndIndependentCommit(t *testing.T) {
	l, r, _ := runtimeFixture(t, true)
	j, e := OpenJournal(l.root + "/journal")
	mustCustody(t, e)
	defer j.Close()
	r.State = "reverted"
	mustCustody(t, j.publish(r))
	other, e := OpenJournal(l.root + "/journal")
	mustCustody(t, e)
	defer other.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- j.CommitTerminal(t.Context(), func(JournalRecord) error { close(entered); <-release; return nil })
	}()
	<-entered
	waiter, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	calls := 0
	e = other.CommitTerminal(waiter, func(JournalRecord) error { calls++; return nil })
	if !errors.Is(e, context.DeadlineExceeded) || calls != 0 {
		t.Fatal("waiter bypassed flock", e, calls)
	}
	fire := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		fire <- other.Fire(t.Context(), r.ID, func() time.Time { return time.Unix(200, 0) }, func(JournalRecord) error { t.Error("terminal fire restored"); return nil })
	}()
	<-started
	select {
	case e := <-fire:
		t.Fatal("Fire bypassed lock", e)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	mustCustody(t, <-done)
	mustCustody(t, <-fire)
}
func TestCommitTerminalCustodyAndCancellation(t *testing.T) {
	for _, fault := range []string{"absent", "missing-lock", "malformed", "cancel-at-sync", "callback"} {
		t.Run(fault, func(t *testing.T) {
			l, r, _ := runtimeFixture(t, true)
			j, e := OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			defer j.Close()
			r.State = "confirmed"
			mustCustody(t, j.publish(r))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			switch fault {
			case "absent":
				mustCustody(t, os.Remove(l.root+"/journal/state"))
			case "missing-lock":
				mustCustody(t, os.Remove(l.root+"/journal/lock"))
			case "malformed":
				mustCustody(t, os.WriteFile(l.root+"/journal/state", []byte(`{}`), 0600))
			case "cancel-at-sync":
				j.syncDirectory = func() error { cancel(); return nil }
			}
			sentinel := errors.New("callback failed")
			e = j.CommitTerminal(ctx, func(JournalRecord) error { calls++; return sentinel })
			if e == nil {
				t.Fatal("fault accepted")
			}
			want := 0
			if fault == "callback" {
				want = 1
				if !errors.Is(e, sentinel) {
					t.Fatal(e)
				}
			}
			if calls != want {
				t.Fatal(calls)
			}
		})
	}
}

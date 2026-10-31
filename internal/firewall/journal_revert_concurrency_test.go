package firewall

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestJournalManualContention(t *testing.T) {
	for _, winner := range []string{"confirm", "revert", "fire"} {
		t.Run(winner, func(t *testing.T) {
			j, r := journalFixture(t)
			ctx := context.Background()
			if err := j.Arm(ctx, r); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			invoke := func(kind string, block bool) error {
				adapter := func() {
					if block {
						close(entered)
						<-release
					} else {
						t.Errorf("losing %s adapter called", kind)
					}
				}
				switch kind {
				case "confirm":
					return j.Confirm(ctx, r.ID, manualClock(99), func(string) error { adapter(); return nil })
				case "revert":
					return j.Revert(ctx, r.ID, manualClock(99), func(JournalRecord) error { adapter(); return nil })
				default:
					return j.Fire(ctx, r.ID, manualClock(100), func(JournalRecord) error { adapter(); return nil })
				}
			}
			first := make(chan error, 1)
			go func() { first <- invoke(winner, true) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("winner failed to enter locked adapter")
			}
			// Each locked call opens its own descriptor, including same-process calls.
			waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			err := j.locked(waitCtx, func() error { return nil })
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lock not held: %v", err)
			}
			started, done := make(chan struct{}, 2), make(chan error, 2)
			for _, kind := range []string{"confirm", "revert", "fire"} {
				if kind == winner {
					continue
				}
				go func() { started <- struct{}{}; done <- invoke(kind, false) }()
			}
			<-started
			<-started
			select {
			case err := <-done:
				t.Fatalf("contender escaped lock: %v", err)
			default:
			}
			close(release)
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			for range 2 {
				err := <-done
				if err != nil {
					requireManualNotFound(t, err)
				}
			}
			got, err := j.read()
			want := "reverted"
			if winner == "confirm" {
				want = "confirmed"
			}
			if err != nil || got.State != want {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestJournalManualClockUnderLock(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() { held <- j.locked(ctx, func() error { close(entered); <-release; return nil }) }()
	<-entered
	// A cancelled waiter proves that no clock or adapter is invoked before flock.
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	err := j.Revert(waitCtx, r.ID, func() time.Time { t.Error("clock before flock"); return time.Unix(99, 0) }, func(JournalRecord) error { t.Error("restore before flock"); return nil })
	close(release)
	if e := <-held; e != nil {
		t.Fatal(e)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	requireManualNotFound(t, j.Revert(ctx, r.ID, manualClock(100), func(JournalRecord) error { t.Error("expired restore"); return nil }))
}

func TestJournalManualDoesNotUndoCommittedConfirmation(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	syncs := 0
	j.syncDirectory = func() error {
		syncs++
		if syncs == 2 {
			return unix.EIO
		}
		return j.dir.Sync()
	}
	err := j.Confirm(ctx, r.ID, manualClock(99), func(string) error { t.Error("unproven timer stop"); return nil })
	if !errors.Is(err, ErrConfirmationCommitted) {
		t.Fatal(err)
	}
	requireManualNotFound(t, j.Revert(ctx, r.ID, manualClock(99), func(JournalRecord) error { t.Error("confirmed restored"); return nil }))
	j.syncDirectory = func() error { return unix.EIO }
	if err := j.Fire(ctx, r.ID, manualClock(100), func(JournalRecord) error { t.Error("confirmed fire restored"); return nil }); !errors.Is(err, ErrConfirmationCommitted) {
		t.Fatal(err)
	}
}

package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/rpcerr"
)

func manualClock(sec int64) func() time.Time {
	return func() time.Time { return time.Unix(sec, 0) }
}

func requireManualNotFound(t *testing.T, err error) {
	t.Helper()
	var coded *rpcerr.CodedError
	if !errors.As(err, &coded) || coded.Code != "not_found" {
		t.Fatalf("want not_found, got %v", err)
	}
}

func TestJournalManualAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, state   string
		now           int64
		wrong, accept bool
	}{
		{"armed", "armed", 99, false, true},
		{"confirming", "confirming", 99, false, true},
		{"exact-deadline", "armed", 100, false, false},
		{"expired-confirming", "confirming", 101, false, false},
		{"interrupted-expired", "reverting", 101, false, true},
		{"interrupted-before", "reverting", 99, false, true},
		{"wrong-id", "armed", 99, true, false},
		{"wrong-interrupted", "reverting", 101, true, false},
		{"confirmed", "confirmed", 99, false, false},
		{"reverted", "reverted", 99, false, false},
		{"acknowledged", "acknowledged", 99, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j, r := journalFixture(t)
			r.State = tc.state
			if err := j.publish(r); err != nil {
				t.Fatal(err)
			}
			id := r.ID
			if tc.wrong {
				id = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
			}
			calls := 0
			err := j.Revert(context.Background(), id, manualClock(tc.now), func(got JournalRecord) error {
				calls++
				want := r
				want.State = "reverting"
				if got != want {
					t.Errorf("metadata: %+v want %+v", got, want)
				}
				return nil
			})
			want := r
			if tc.accept {
				if err != nil || calls != 1 {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				want.State = "reverted"
				requireManualNotFound(t, j.Revert(context.Background(), id, manualClock(102), func(JournalRecord) error { t.Error("terminal retry restored"); return nil }))
			} else {
				requireManualNotFound(t, err)
				if calls != 0 {
					t.Fatal(calls)
				}
			}
			got, err := j.read()
			if err != nil || got != want {
				t.Fatalf("state=%+v want=%+v err=%v", got, want, err)
			}
		})
	}
}

func TestJournalManualRevert(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := j.Revert(ctx, r.ID, func() time.Time { return time.Unix(99, 0) }, func(got JournalRecord) error {
		calls++
		want := r
		want.State = "reverting"
		if got != want {
			t.Fatalf("metadata changed: got %+v want %+v", got, want)
		}
		visible, err := j.read()
		if err != nil || visible != want {
			t.Fatalf("recovery intent missing before restore: %+v %v", visible, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := j.read()
	r.State = "reverted"
	if err != nil || got != r || calls != 1 {
		t.Fatalf("state=%+v calls=%d err=%v", got, calls, err)
	}
	if err := j.Fire(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, func(JournalRecord) error { t.Error("second restoration"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := j.Arm(ctx, r); err == nil {
		t.Fatal("terminal record reused")
	}
}

func TestJournalManualFailureReplay(t *testing.T) {
	for _, phase := range []string{"restore", "marker", "intent-sync", "terminal-before-rename", "terminal-sync", "cancel-before", "cancel-after-intent", "cancel-after-restore"} {
		t.Run(phase, func(t *testing.T) {
			j, r := journalFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := j.Arm(ctx, r); err != nil {
				t.Fatal(err)
			}
			clock := manualClock(99)
			if phase == "cancel-before" {
				clock = func() time.Time { cancel(); return time.Unix(99, 0) }
			}
			syncs, calls := 0, 0
			j.syncDirectory = func() error {
				syncs++
				if phase == "intent-sync" && syncs == 1 || phase == "terminal-sync" && syncs == 2 {
					return unix.EIO
				}
				if phase == "cancel-after-intent" && syncs == 1 {
					cancel()
				}
				return j.dir.Sync()
			}
			err := j.Revert(ctx, r.ID, clock, func(got JournalRecord) error {
				calls++
				if phase == "restore" {
					return unix.EIO
				}
				if phase == "cancel-after-intent" {
					return ctx.Err()
				}
				if phase == "marker" {
					return os.WriteFile(filepath.Join(j.root, "absent", "marker"), []byte(got.ID), 0600)
				}
				if phase == "terminal-before-rename" {
					return os.Mkdir(filepath.Join(j.root, "state.next"), 0700)
				}
				if phase == "cancel-after-restore" {
					cancel()
				}
				return nil
			})
			if phase == "cancel-after-restore" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("injected failure accepted")
			}
			if (phase == "cancel-before" || phase == "intent-sync") && calls != 0 {
				t.Fatal("restoration before durable intent")
			}
			if phase == "terminal-before-rename" {
				if err := os.Remove(filepath.Join(j.root, "state.next")); err != nil {
					t.Fatal(err)
				}
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenJournal(j.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			state, err := reopened.read()
			if err != nil {
				t.Fatal(err)
			}
			want := r
			switch phase {
			case "cancel-before":
				want.State = "armed"
			case "terminal-sync", "cancel-after-restore":
				want.State = "reverted"
			default:
				want.State = "reverting"
			}
			if state != want {
				t.Fatalf("state=%+v want=%+v", state, want)
			}
			retries := 0
			clock = manualClock(101)
			if phase == "cancel-before" {
				clock = manualClock(99)
			}
			err = reopened.Revert(context.Background(), r.ID, clock, func(got JournalRecord) error {
				retries++
				want.State = "reverting"
				if got != want {
					t.Error(got)
				}
				return nil
			})
			if state.State == "reverted" {
				requireManualNotFound(t, err)
				if retries != 0 {
					t.Fatal(retries)
				}
			} else if err != nil || retries != 1 {
				t.Fatalf("retry=%d err=%v", retries, err)
			}
			if err := reopened.Fire(context.Background(), r.ID, manualClock(102), func(JournalRecord) error { t.Error("terminal fire restored"); return nil }); err != nil {
				t.Fatal(err)
			}
			if err := reopened.Reconcile(context.Background(), func(JournalRecord) error { return unix.EIO }); !errors.Is(err, unix.EIO) {
				t.Fatal(err)
			}
			state, err = reopened.read()
			if err != nil || state.State != "reverted" {
				t.Fatalf("%+v %v", state, err)
			}
			if err := reopened.Reconcile(context.Background(), func(got JournalRecord) error {
				if got.ID != r.ID {
					t.Error(got)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			requireManualNotFound(t, reopened.Revert(context.Background(), r.ID, manualClock(99), func(JournalRecord) error { t.Error("acknowledged restored"); return nil }))
		})
	}
}

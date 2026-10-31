package firewall

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"testing"
	"time"
)

func TestJournalConfirmationCommitBoundary(t *testing.T) {
	for _, phase := range []string{"prepare-cancel", "commit-sync-error", "commit-cancel"} {
		t.Run(phase, func(t *testing.T) {
			j, r := journalFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := j.Arm(ctx, r); err != nil {
				t.Fatal(err)
			}
			calls := 0
			j.syncDirectory = func() error {
				calls++
				if calls == 1 && phase == "prepare-cancel" {
					cancel()
				}
				if calls == 2 && phase == "commit-sync-error" {
					return unix.EIO
				}
				if calls == 2 && phase == "commit-cancel" {
					cancel()
				}
				return j.dir.Sync()
			}
			stopped := false
			err := j.Confirm(ctx, r.ID, func() time.Time { return time.Unix(99, 0) }, func(string) error { stopped = true; return nil })
			switch phase {
			case "prepare-cancel":
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrConfirmationCommitted) || stopped {
					t.Fatalf("err=%v stopped=%v", err, stopped)
				}
			case "commit-sync-error":
				if !errors.Is(err, unix.EIO) || !errors.Is(err, ErrConfirmationCommitted) || stopped {
					t.Fatalf("err=%v stopped=%v", err, stopped)
				}
			case "commit-cancel":
				if err != nil || !stopped {
					t.Fatalf("err=%v stopped=%v", err, stopped)
				}
			}
			reopened, err := OpenJournal(j.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if phase == "commit-sync-error" {
				reopened.syncDirectory = func() error { return unix.EIO }
				if err = reopened.Fire(context.Background(), r.ID, func() time.Time { return time.Unix(101, 0) }, func(JournalRecord) error { t.Fatal("committed confirmation restored"); return nil }); !errors.Is(err, ErrConfirmationCommitted) {
					t.Fatalf("unproven commit silently accepted: %v", err)
				}
				if err = reopened.Reconcile(context.Background(), func(JournalRecord) error { t.Fatal("unproven commit reconciled"); return nil }); !errors.Is(err, ErrConfirmationCommitted) {
					t.Fatal(err)
				}
				reopened.syncDirectory = nil
			}
			restores := 0
			if err = reopened.Fire(context.Background(), r.ID, func() time.Time { return time.Unix(101, 0) }, func(JournalRecord) error { restores++; return nil }); err != nil {
				t.Fatal(err)
			}
			expected := 0
			if phase == "prepare-cancel" {
				expected = 1
			}
			if restores != expected {
				t.Fatalf("restores=%d want=%d", restores, expected)
			}
		})
	}
}

func TestJournalPreparedConfirmationCrash(t *testing.T) {
	j, r := journalFixture(t)
	if err := j.Arm(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	child := startJournalChild(t, j.root, "prepare-crash")
	waitJournalFile(t, j.root+"/ready")
	child.cancel()
	waitJournalChild(t, child, true)
	reopened, err := OpenJournal(j.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.read()
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "confirming" {
		t.Fatal(got)
	}
	calls := 0
	if err = reopened.Fire(context.Background(), r.ID, func() time.Time { return time.Unix(101, 0) }, func(got JournalRecord) error {
		calls++
		if got.ID != r.ID {
			t.Fatal(got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

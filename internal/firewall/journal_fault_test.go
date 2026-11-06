package firewall

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"testing"
	"time"
)

// These regressions reproduce the independent review's faults on real files.
func TestJournalFailedConfirmDirectorySyncRestoresAfterReopen(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	j.syncDirectory = func() error { return unix.EIO }
	stopped := false
	err := j.Confirm(ctx, r.ID, func() time.Time { return time.Unix(99, 0) }, func(string) error { stopped = true; return nil })
	if !errors.Is(err, unix.EIO) || stopped {
		t.Fatalf("err=%v stopped=%v", err, stopped)
	}
	reopened, err := OpenJournal(j.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restored := false
	err = reopened.Fire(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, func(got JournalRecord) error {
		restored = true
		if got.ID != r.ID {
			t.Fatal(got)
		}
		return nil
	})
	got, re := reopened.read()
	if re != nil {
		t.Fatal(re)
	}
	if err != nil || !restored || got.State != "reverted" {
		t.Fatalf("err=%v restored=%v state=%s", err, restored, got.State)
	}
}

func TestJournalCancellationBeforeRecoveryPublication(t *testing.T) {
	j, r := journalFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	err := j.Fire(ctx, r.ID, func() time.Time { cancel(); return time.Unix(101, 0) }, func(JournalRecord) error { t.Fatal("cancelled recovery invoked"); return nil })
	got, re := j.read()
	if re != nil {
		t.Fatal(re)
	}
	if !errors.Is(err, context.Canceled) || got.State != "armed" {
		t.Fatalf("err=%v state=%s", err, got.State)
	}
}

func TestJournalCancellationBeforeConfirmationPublication(t *testing.T) {
	j, r := journalFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	stopped := false
	err := j.Confirm(ctx, r.ID, func() time.Time { cancel(); return time.Unix(99, 0) }, func(string) error { stopped = true; return nil })
	got, re := j.read()
	if re != nil {
		t.Fatal(re)
	}
	if !errors.Is(err, context.Canceled) || stopped || got.State != "armed" {
		t.Fatalf("err=%v stopped=%v state=%s", err, stopped, got.State)
	}
}

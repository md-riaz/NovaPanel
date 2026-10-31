package firewall

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJournalAcknowledgementRetryEstablishesDurability(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := j.Confirm(ctx, r.ID, func() time.Time { return time.Unix(r.Deadline-1, 0) }, func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("directory sync")
	calls := 0
	j.syncDirectory = func() error {
		calls++
		if calls == 1 {
			return nil
		}
		return fault
	}
	commits := 0
	if err := j.Reconcile(ctx, func(JournalRecord) error { commits++; return nil }); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if err := j.Reconcile(ctx, func(JournalRecord) error { commits++; return nil }); !errors.Is(err, fault) {
		t.Fatal("visible acknowledgement accepted without durability", err)
	}
	j.syncDirectory = nil
	if err := j.Reconcile(ctx, func(JournalRecord) error { commits++; return nil }); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatal("recommitted acknowledged record", commits)
	}
}

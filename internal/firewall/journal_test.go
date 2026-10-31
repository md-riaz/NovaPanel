package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func journalFixture(t *testing.T) (*Journal, JournalRecord) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j, JournalRecord{Version: 1, ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Unit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: 100, SnapshotSHA256: strings.Repeat("a", 64), PriorEnabled: true, State: "armed"}
}

func TestJournalClockSampledUnderLock(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- j.locked(ctx, func() error { close(entered); <-release; return nil }) }()
	<-entered
	sampled := make(chan struct{}, 1)
	result := make(chan error, 1)
	go func() {
		result <- j.Confirm(ctx, r.ID, func() time.Time { sampled <- struct{}{}; return time.Unix(100, 0) }, func(string) error { t.Error("expired stop"); return nil })
	}()
	select {
	case <-sampled:
		t.Error("clock sampled before lock")
	case <-time.After(time.Millisecond * 30):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("expired confirmation")
	}
}

func TestJournalStaleTemporaryRecovery(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.root, "state.next"), []byte("incomplete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := j.Fire(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, func(JournalRecord) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(j.root, "state.next")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestJournalReconcileRetainsTerminalUntilCommit(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := j.Fire(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, func(got JournalRecord) error {
		if got.SnapshotSHA256 != r.SnapshotSHA256 || !got.PriorEnabled {
			t.Fatal(got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("commit")
	if err := j.Reconcile(ctx, func(got JournalRecord) error {
		if got.State != "reverted" {
			t.Fatal(got)
		}
		return failed
	}); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	calls := 0
	if err := j.Reconcile(ctx, func(got JournalRecord) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := j.Reconcile(ctx, func(got JournalRecord) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestJournalConfirmDurableBeforeStop(t *testing.T) {
	j, rec := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, rec); err != nil {
		t.Fatal(err)
	}
	stopErr := errors.New("stop failed")
	called := false
	err := j.Confirm(ctx, rec.ID, func() time.Time { return time.Unix(99, 0) }, func(unit string) error {
		called = true
		data, e := os.ReadFile(filepath.Join(j.root, "state"))
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(data), `"state":"confirmed"`) || unit != rec.Unit {
			t.Fatal(string(data), unit)
		}
		return stopErr
	})
	if !called || !errors.Is(err, stopErr) {
		t.Fatal(called, err)
	}
	if err := j.Fire(ctx, rec.ID, func() time.Time { return time.Unix(101, 0) }, func(JournalRecord) error { t.Fatal("confirmed restored"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := j.Arm(ctx, rec); err == nil {
		t.Fatal("terminal state reused")
	}
}

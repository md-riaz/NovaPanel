package firewall

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	execpkg "novapanel/internal/exec"
)

type memoryPending struct {
	mu sync.Mutex
	p  *Pending
}

func (s *memoryPending) WithPending(ctx context.Context, id string, fn func(Pending) (bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil || s.p.ID != id {
		return notFound()
	}
	consume, err := fn(*s.p)
	if consume {
		s.p = nil
	}
	return err
}

type recordingRunner struct {
	calls [][]string
	fail  bool
}

func (r *recordingRunner) Run(_ context.Context, prog string, args []string, _ execpkg.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{prog}, args...))
	if r.fail {
		return nil, nil, 1, nil
	}
	return nil, nil, 0, nil
}

type scratchSnapshot struct {
	path     string
	restores int
	fail     bool
}

func (s *scratchSnapshot) RestoreAndMark(_ context.Context, p Pending) error {
	if s.fail {
		return os.ErrPermission
	}
	s.restores++
	return os.WriteFile(s.path, []byte(p.ID), 0600)
}
func fixture(t *testing.T) (*Recovery, *memoryPending, *recordingRunner, *scratchSnapshot, *time.Time) {
	t.Helper()
	now := time.Unix(2000, 0)
	store := &memoryPending{p: &Pending{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SnapshotPath: t.TempDir(), RevertUnit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: now.Add(time.Minute)}}
	runner := &recordingRunner{}
	snap := &scratchSnapshot{path: filepath.Join(t.TempDir(), "reverted")}
	return &Recovery{Store: store, Runner: runner, Snapshot: snap, Now: func() time.Time { return now }}, store, runner, snap, &now
}
func TestRevertConsumesOnceWithoutCancellingTimer(t *testing.T) {
	r, s, run, snap, _ := fixture(t)
	id := s.p.ID
	if err := r.Revert(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if s.p != nil || snap.restores != 1 || len(run.calls) != 0 {
		t.Fatal("incorrect restoration", run.calls)
	}
	if err := r.Confirm(context.Background(), id); err == nil {
		t.Fatal("replay accepted")
	}
}
func TestFailuresRetainPendingForRecovery(t *testing.T) {
	r, s, run, snap, now := fixture(t)
	id := s.p.ID
	run.fail = true
	if err := r.Confirm(context.Background(), id); err == nil || s.p == nil {
		t.Fatal("cancel failure lost pending")
	}
	snap.fail = true
	if err := r.Revert(context.Background(), id); err == nil || s.p == nil {
		t.Fatal("restore failure lost pending")
	}
	*now = s.p.Deadline
	if err := r.Confirm(context.Background(), id); err == nil || len(run.calls) != 0 {
		t.Fatal("expired confirm cancelled timer")
	}
	if err := r.Revert(context.Background(), id); err == nil {
		t.Fatal("expired client revert accepted")
	}
	if err := r.Fire(context.Background(), id); err == nil || s.p == nil {
		t.Fatal("deadman failure lost pending")
	}
	snap.fail = false
	if err := r.Fire(context.Background(), id); err != nil || s.p != nil {
		t.Fatal("deadman retry failed", err)
	}
}
func TestConcurrentConfirmAndRevertConsumeOnce(t *testing.T) {
	r, s, run, snap, _ := fixture(t)
	id := s.p.ID
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = r.Confirm(context.Background(), id)
			} else {
				err = r.Revert(context.Background(), id)
			}
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success != 1 || s.p != nil || len(run.calls)+snap.restores != 1 {
		t.Fatalf("success=%d cancel=%d restore=%d", success, len(run.calls), snap.restores)
	}
}
func TestRecoveryRejectsTamperedOrMissingState(t *testing.T) {
	r, s, run, snap, _ := fixture(t)
	id := s.p.ID
	s.p.RevertUnit = "other.service;reboot"
	if err := r.Confirm(context.Background(), id); err == nil || len(run.calls) != 0 || snap.restores != 0 {
		t.Fatal("tampered unit accepted")
	}
	if err := r.Confirm(context.Background(), "--force"); err == nil {
		t.Fatal("invalid ID accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Confirm(ctx, id); err == nil {
		t.Fatal("cancelled context accepted")
	}
	r.Runner = nil
	if err := r.Fire(context.Background(), id); err == nil {
		t.Fatal("missing dependency accepted")
	}
}

func TestDeadmanRestoresAndConsumesAtDeadline(t *testing.T) {
	r, s, run, snap, now := fixture(t)
	id := s.p.ID
	if err := r.Fire(context.Background(), id); err == nil {
		t.Fatal("early fire accepted")
	}
	*now = now.Add(time.Minute)
	if err := r.Fire(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(snap.path)
	if err != nil || string(b) != id || s.p != nil || len(run.calls) != 0 {
		t.Fatalf("restore/marker/consume: %q %v", b, err)
	}
	if err := r.Confirm(context.Background(), id); err == nil {
		t.Fatal("replay accepted")
	}
}

package firewall

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	execpkg "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
)

func TestJournalRecoveryRevertCustodyAndDestinationRefusal(t *testing.T) {
	for _, fault := range []string{"digest", "enabled", "inventory", "tamper", "symlink", "hardlink", "owner", "gid", "unsafe", "missing-rules", "missing-markers", "missing-restore"} {
		t.Run(fault, func(t *testing.T) {
			s, r, f := recoveryFixture(t, true)
			snapshot := s.layout.root + "/snapshots/" + r.ID
			leaf := snapshot + "/ufw/user.rules"
			switch fault {
			case "digest":
				r.SnapshotSHA256 = strings.Repeat("a", 64)
				mustCustody(t, s.journal.publish(r))
			case "enabled":
				r.PriorEnabled = false
				mustCustody(t, s.journal.publish(r))
			case "inventory":
				mustCustody(t, os.WriteFile(snapshot+"/extra", []byte("x"), 0600))
			case "tamper":
				mustCustody(t, os.WriteFile(leaf, []byte("changed"), 0600))
			case "symlink":
				mustCustody(t, os.Remove(leaf))
				mustCustody(t, os.Symlink("before.rules", leaf))
			case "hardlink":
				mustCustody(t, os.Link(leaf, snapshot+"/linked"))
			case "owner":
				mustCustody(t, os.Chown(leaf, 65534, 0))
			case "gid":
				mustCustody(t, os.Chown(s.layout.rules+"/user.rules", 0, 65534))
			case "unsafe":
				mustCustody(t, os.Chmod(s.layout.rules, 0777))
			case "missing-rules":
				mustCustody(t, os.Rename(s.layout.rules, s.layout.rules+".gone"))
			case "missing-markers":
				mustCustody(t, os.Remove(s.layout.root+"/restore/markers"))
			case "missing-restore":
				mustCustody(t, os.Rename(s.layout.root+"/restore", s.layout.root+"/restore.gone"))
			}
			if e := s.Revert(t.Context(), r.ID); e == nil {
				t.Fatal("unsafe input admitted")
			}
			if len(f.calls) != 0 {
				t.Fatal("command before admission")
			}
			got, e := s.journal.read()
			mustCustody(t, e)
			if got.ID != r.ID || got.State != "reverting" {
				t.Fatal(got)
			}
		})
	}
}
func TestJournalRecoveryOpenWaitHonorsContext(t *testing.T) {
	s, _, _ := recoveryFixture(t, true)
	held, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- s.journal.locked(t.Context(), func() error { close(held); <-release; return nil }) }()
	<-held
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	next, e := openInstalledJournalRecovery(ctx, s.layout, s.now, s.manager.runner, s.runner)
	if next != nil {
		next.Close()
		t.Fatal("opener escaped lock")
	}
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	close(release)
	mustCustody(t, <-done)
}
func TestJournalRecoveryDetachedStopActualTimeout(t *testing.T) {
	s, r, _ := recoveryFixture(t, true)
	s.manager.runner = recoveryBlockingManager{}
	started := time.Now()
	e := s.Confirm(t.Context(), r.ID)
	if !errors.Is(e, ErrConfirmationCommitted) || !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if elapsed := time.Since(started); elapsed < 9*time.Second || elapsed > 15*time.Second {
		t.Fatal("completion not bounded", elapsed)
	}
	got, e := s.journal.read()
	mustCustody(t, e)
	if got.State != "confirmed" {
		t.Fatal(got)
	}
	mustCustody(t, recoverInstalled(t.Context(), r.ID, s.layout, func() time.Time { return time.Unix(101, 0) }, &restoreRunner{}))
}

type recoveryBlockingManager struct{}

func (recoveryBlockingManager) Run(ctx context.Context, _ string, _ []string, _ execpkg.RunOpts) ([]byte, []byte, int, error) {
	<-ctx.Done()
	return nil, nil, 1, ctx.Err()
}

func TestJournalRecoveryMissingRootRemainsFailure(t *testing.T) {
	s, r, _ := recoveryFixture(t, true)
	mustCustody(t, os.Rename(s.layout.root+"/journal", s.layout.root+"/journal.gone"))
	defer os.Rename(s.layout.root+"/journal.gone", s.layout.root+"/journal")
	for _, op := range []func(context.Context, string) error{s.Confirm, s.Revert} {
		e := op(t.Context(), r.ID)
		var coded *rpcerr.CodedError
		if e == nil || errors.As(e, &coded) {
			t.Fatal("missing layout not rejected at admission", e)
		}
	}
}

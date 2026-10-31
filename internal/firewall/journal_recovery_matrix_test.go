package firewall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
)

func recoveryFixture(t *testing.T, enabled bool) (*JournalRecovery, JournalRecord, *restoreRunner) {
	t.Helper()
	l, r, f := runtimeFixture(t, enabled)
	base := newManagerFixture(t, r)
	runner := interceptManager{base: base, hook: func(_ context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
		if p == "/usr/bin/systemctl" {
			if !reflect.DeepEqual(a, []string{"--system", "stop", r.Unit + ".timer"}) {
				t.Fatal(a)
			}
			return nil, nil, 0, nil, true
		}
		return nil, nil, 0, nil, false
	}}
	s, e := openInstalledJournalRecovery(t.Context(), l, func() time.Time { return time.Unix(90, 0) }, runner, f)
	mustCustody(t, e)
	t.Cleanup(func() { mustCustody(t, s.Close()) })
	return s, r, f
}
func requireRecoveryNotFound(t *testing.T, e error) { t.Helper(); requireManualNotFound(t, e) }
func TestJournalRecoveryAdmission(t *testing.T) {
	for _, op := range []string{"confirm", "revert"} {
		for _, state := range []string{"armed", "confirming", "confirmed", "reverting", "reverted", "acknowledged", "absent", "malformed", "wrong", "deadline"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				s, r, f := recoveryFixture(t, true)
				id := r.ID
				switch state {
				case "absent":
					mustCustody(t, os.Remove(s.layout.root+"/journal/state"))
				case "malformed":
					id = "not-an-id"
				case "wrong":
					id = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
				case "deadline":
					s.now = func() time.Time { return time.Unix(100, 0) }
				default:
					r.State = state
					mustCustody(t, s.journal.publish(r))
				}
				var e error
				if op == "confirm" {
					e = s.Confirm(t.Context(), id)
				} else {
					e = s.Revert(t.Context(), id)
				}
				admit := state == "armed" || state == "confirming" || op == "confirm" && state == "confirmed" || op == "revert" && state == "reverting"
				if admit {
					mustCustody(t, e)
				} else {
					requireRecoveryNotFound(t, e)
					if len(f.calls) != 0 {
						t.Fatal("refusal restored")
					}
				}
			})
		}
	}
}
func TestJournalRecoveryConfirmCommitAndCancellation(t *testing.T) {
	for _, fault := range []string{"none", "prepare", "commit-sync", "before-rename", "after-commit", "stop-error", "timeout", "active", "malformed-readback"} {
		t.Run(fault, func(t *testing.T) {
			s, r, f := recoveryFixture(t, true)
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), recoveryContextKey{}, "request"))
			defer cancel()
			syncs := 0
			s.journal.syncDirectory = func() error {
				syncs++
				if fault == "prepare" && syncs == 1 || fault == "commit-sync" && syncs == 2 {
					return unix.EIO
				}
				if fault == "before-rename" && syncs == 1 || fault == "after-commit" && syncs == 2 {
					cancel()
				}
				return s.journal.dir.Sync()
			}
			base := newManagerFixture(t, r)
			calls := 0
			s.manager.runner = interceptManager{base: base, hook: func(c context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
				calls++
				if c.Err() != nil || c.Value(recoveryContextKey{}) != "request" {
					t.Fatal("completion lost context values or inherited cancellation")
				}
				d, ok := c.Deadline()
				if !ok || time.Until(d) > 10*time.Second || time.Until(d) <= 0 {
					t.Fatal("unbounded completion")
				}
				got, e := s.journal.read()
				mustCustody(t, e)
				if got.State != "confirmed" || syncs < 2 {
					t.Fatal("effect before durable commit")
				}
				if p == "/usr/bin/systemctl" {
					if fault == "stop-error" {
						return nil, nil, 1, unix.EIO, true
					}
					if fault == "timeout" {
						return nil, nil, 1, context.DeadlineExceeded, true
					}
					return nil, nil, 0, nil, true
				}
				if fault == "malformed-readback" {
					return []byte(`{}`), nil, 0, nil, true
				}
				if fault == "active" {
					return managerResponse("a(ssssssouso)", []any{[]any{[]any{r.Unit + ".timer", "", "loaded", "active", "waiting", "", "/org/freedesktop/systemd1/unit/fixture", 0, "", "/"}}}), nil, 0, nil, true
				}
				return nil, nil, 0, nil, false
			}}
			e := s.Confirm(ctx, r.ID)
			switch fault {
			case "none", "after-commit":
				mustCustody(t, e)
			case "prepare":
				if !errors.Is(e, unix.EIO) || errors.Is(e, ErrConfirmationCommitted) || calls != 0 {
					t.Fatal(e, calls)
				}
			case "before-rename":
				if !errors.Is(e, context.Canceled) || errors.Is(e, ErrConfirmationCommitted) || calls != 0 {
					t.Fatal(e, calls)
				}
			default:
				if !errors.Is(e, ErrConfirmationCommitted) {
					t.Fatal(e)
				}
				if fault == "commit-sync" && (!errors.Is(e, unix.EIO) || calls != 0) {
					t.Fatal(e, calls)
				}
				if fault == "stop-error" && !errors.Is(e, unix.EIO) {
					t.Fatal(e)
				}
				if fault == "timeout" && !errors.Is(e, context.DeadlineExceeded) {
					t.Fatal(e)
				}
			}
			if len(f.calls) != 0 {
				t.Fatal("confirmation ran UFW")
			}
		})
	}
}

type recoveryContextKey struct{}

func TestJournalRecoveryInstalledRevertReplay(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, fault := range []string{"none", "first-command", "second-command", "cancellation", "marker", "terminal"} {
			if !enabled && fault == "second-command" {
				continue
			}
			t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled]+"/"+fault, func(t *testing.T) {
				s, r, _ := recoveryFixture(t, enabled)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				f := &recoveryUFW{fail: 0}
				s.runner = f
				switch fault {
				case "first-command":
					f.fail = 1
				case "second-command":
					f.fail = 2
				case "cancellation":
					f.cancel = cancel
				case "marker":
					mustCustody(t, os.Mkdir(s.layout.root+"/restore/markers/"+r.ID+".json", 0700))
				case "terminal":
					n := 0
					s.journal.syncDirectory = func() error {
						n++
						if n == 2 {
							return unix.EIO
						}
						return s.journal.dir.Sync()
					}
				}
				e := s.Revert(ctx, r.ID)
				if fault == "none" {
					mustCustody(t, e)
				} else {
					if e == nil {
						t.Fatal("fault succeeded")
					}
					got, x := s.journal.read()
					mustCustody(t, x)
					if got.ID != r.ID || got.Deadline != r.Deadline || (got.State != "reverting" && !(fault == "terminal" && got.State == "reverted")) {
						t.Fatal(got)
					}
					if fault == "marker" {
						mustCustody(t, os.Remove(s.layout.root+"/restore/markers/"+r.ID+".json"))
					}
					// A new independent handle and expired clock replay interrupted recovery.
					next, x := openInstalledJournalRecovery(t.Context(), s.layout, func() time.Time { return time.Unix(101, 0) }, newManagerFixture(t, r), &restoreRunner{})
					mustCustody(t, x)
					defer next.Close()
					if fault == "terminal" {
						requireRecoveryNotFound(t, next.Revert(t.Context(), r.ID))
						mustCustody(t, recoverInstalled(t.Context(), r.ID, s.layout, next.now, next.runner))
					} else {
						mustCustody(t, next.Revert(t.Context(), r.ID))
					}
				}
				got, x := s.journal.read()
				mustCustody(t, x)
				if got.State != "reverted" {
					t.Fatal(got)
				}
				for i, n := range snapshotNames[4:] {
					leaf := strings.TrimPrefix(n, "ufw/")
					b, x := os.ReadFile(s.layout.rules + "/" + leaf)
					mustCustody(t, x)
					if !bytes.Equal(b, custodyInput().Rules[leaf]) {
						t.Fatal(leaf)
					}
					st, x := os.Stat(s.layout.rules + "/" + leaf)
					mustCustody(t, x)
					if st.Mode().Perm() != []os.FileMode{0600, 0640, 0644}[i%3] {
						t.Fatal("mode changed")
					}
				}
				marker, x := os.ReadFile(s.layout.root + "/restore/markers/" + r.ID + ".json")
				mustCustody(t, x)
				if !bytes.Contains(marker, []byte(r.SnapshotSHA256)) || !bytes.Contains(marker, []byte(r.ID)) {
					t.Fatal("unbound marker")
				}
				requireRecoveryNotFound(t, s.Revert(t.Context(), r.ID))
				// Terminal refusal must not open installed destinations.
				mustCustody(t, os.Rename(s.layout.rules, s.layout.rules+".gone"))
				requireRecoveryNotFound(t, s.Revert(t.Context(), r.ID))
			})
		}
	}
}

type recoveryUFW struct {
	calls   [][]string
	fail    int
	cancel  context.CancelFunc
	entered chan struct{}
	release chan struct{}
}

func (f *recoveryUFW) Run(ctx context.Context, p string, a []string, _ execpkg.RunOpts) ([]byte, []byte, int, error) {
	if p != "/usr/sbin/ufw" {
		return nil, nil, 1, errors.New("unexpected non-UFW command")
	}
	f.calls = append(f.calls, append([]string{p}, a...))
	if f.entered != nil && len(f.calls) == 1 {
		close(f.entered)
		<-f.release
	}
	if f.cancel != nil {
		f.cancel()
		return nil, nil, 1, ctx.Err()
	}
	if f.fail == len(f.calls) {
		return nil, nil, 1, unix.EIO
	}
	return nil, nil, 0, nil
}
func TestJournalRecoveryWaiterResamplesAndCancels(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancellation"}[cancelWait], func(t *testing.T) {
			s, r, _ := recoveryFixture(t, true)
			var clock atomic.Int64
			clock.Store(90)
			other, e := openInstalledJournalRecovery(t.Context(), s.layout, func() time.Time { return time.Unix(clock.Load(), 0) }, newManagerFixture(t, r), &restoreRunner{})
			mustCustody(t, e)
			defer other.Close()
			held, release := make(chan struct{}), make(chan struct{})
			holder := make(chan error, 1)
			go func() { holder <- s.journal.locked(t.Context(), func() error { close(held); <-release; return nil }) }()
			<-held
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- other.Confirm(ctx, r.ID) }()
			select {
			case e := <-done:
				t.Fatal("waiter escaped lock", e)
			case <-time.After(25 * time.Millisecond):
			}
			if cancelWait {
				cancel()
				if e := <-done; !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				close(release)
			} else {
				clock.Store(100)
				close(release)
				requireRecoveryNotFound(t, <-done)
			}
			mustCustody(t, <-holder)
		})
	}
}
func TestJournalRecoveryManualWinnerSerializesFireAndConfirm(t *testing.T) {
	s, r, _ := recoveryFixture(t, true)
	other, e := openInstalledJournalRecovery(t.Context(), s.layout, s.now, newManagerFixture(t, r), &restoreRunner{})
	mustCustody(t, e)
	defer other.Close()
	f := &recoveryUFW{entered: make(chan struct{}), release: make(chan struct{})}
	s.runner = f
	done := make(chan error, 1)
	go func() { done <- s.Revert(t.Context(), r.ID) }()
	<-f.entered
	fire := make(chan error, 1)
	go func() {
		fire <- recoverInstalled(t.Context(), r.ID, s.layout, func() time.Time { return time.Unix(101, 0) }, &restoreRunner{})
	}()
	confirm := make(chan error, 1)
	go func() { confirm <- other.Confirm(t.Context(), r.ID) }()
	select {
	case e := <-fire:
		t.Fatal("Fire bypassed manual lock", e)
	case <-time.After(25 * time.Millisecond):
	}
	close(f.release)
	mustCustody(t, <-done)
	mustCustody(t, <-fire)
	requireRecoveryNotFound(t, <-confirm)
	if !reflect.DeepEqual(f.calls, [][]string{{"/usr/sbin/ufw", "--force", "enable"}, {"/usr/sbin/ufw", "reload"}}) {
		t.Fatal(f.calls)
	}
}

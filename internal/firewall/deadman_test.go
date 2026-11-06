package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	execpkg "novapanel/internal/exec"
)

func TestJournalRejectsTampering(t *testing.T) {
	for _, kind := range []string{"version", "missing", "trailing", "digest", "duplicate", "oversize", "unit", "unknown", "symlink", "hardlink", "mode", "temp-symlink", "root-mode", "root-symlink", "foreign-owner", "lock-replaced"} {
		t.Run(kind, func(t *testing.T) {
			j, r := journalFixture(t)
			ctx := context.Background()
			if err := j.Arm(ctx, r); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(j.root, "state")
			switch kind {
			case "missing":
				b, _ := os.ReadFile(state)
				os.WriteFile(state, []byte(strings.Replace(string(b), `,"prior_enabled":true`, "", 1)), 0600)
			case "trailing":
				b, _ := os.ReadFile(state)
				os.WriteFile(state, append(b, []byte(` {}`)...), 0600)
			case "digest":
				r.SnapshotSHA256 = "not-a-digest"
				b, _ := json.Marshal(r)
				os.WriteFile(state, b, 0600)
			case "version":
				r.Version = 2
				b, _ := json.Marshal(r)
				os.WriteFile(state, b, 0600)
			case "duplicate":
				b, _ := os.ReadFile(state)
				os.WriteFile(state, append([]byte(`{"version":1,`), b[1:]...), 0600)
			case "oversize":
				os.WriteFile(state, []byte(strings.Repeat("x", journalLimit+1)), 0600)
			case "unit":
				r.Unit = "other"
				b, _ := json.Marshal(r)
				os.WriteFile(state, b, 0600)
			case "unknown":
				b, _ := os.ReadFile(state)
				os.WriteFile(state, append([]byte(`{"extra":1,`), b[1:]...), 0600)
			case "symlink":
				os.Rename(state, state+".original")
				os.Symlink(state+".original", state)
			case "hardlink":
				os.Link(state, state+".link")
			case "mode":
				os.Chmod(state, 0640)
			case "temp-symlink":
				os.Symlink(state, filepath.Join(j.root, "state.next"))
			case "root-mode":
				os.Chmod(j.root, 0770)
			case "root-symlink":
				os.Rename(j.root, j.root+".original")
				defer os.Rename(j.root+".original", j.root)
				os.Symlink(j.root+".original", j.root)
				defer os.Remove(j.root)
			case "lock-replaced":
				os.Rename(filepath.Join(j.root, "lock"), filepath.Join(j.root, "lock.old"))
				os.WriteFile(filepath.Join(j.root, "lock"), nil, 0600)
			case "foreign-owner":
				if os.Geteuid() != 0 {
					t.Skip("requires root scratch chown")
				}
				os.Chown(state, 65534, 65534)
			}
			called := false
			if err := j.Confirm(ctx, r.ID, func() time.Time { return time.Unix(99, 0) }, func(string) error { called = true; return nil }); err == nil || called {
				t.Fatalf("tamper accepted %v %v", err, called)
			}
		})
	}
}

func TestJournalDeadlineAndReplay(t *testing.T) {
	j, r := journalFixture(t)
	ctx := context.Background()
	if err := j.Arm(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := j.Confirm(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, func(string) error { t.Fatal("expired stop"); return nil }); err == nil {
		t.Fatal("expired confirm")
	}
	failed := errors.New("marker failed")
	if err := j.Fire(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, func(JournalRecord) error { return failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	reopened, err := OpenJournal(j.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	calls := 0
	restore := func(got JournalRecord) error {
		calls++
		if got.ID != r.ID || got.State != "reverting" {
			t.Fatal(got)
		}
		return nil
	}
	if err = reopened.Fire(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, restore); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Fire(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, restore); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestJournalScratchHelper(t *testing.T) {
	root := os.Getenv("JOURNAL_TEST_ROOT")
	if root == "" {
		return
	}
	action := os.Getenv("JOURNAL_TEST_ACTION")
	if err := os.WriteFile(filepath.Join(root, "started-"+action), nil, 0600); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	pending := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	barrier := func() error {
		if err := os.WriteFile(filepath.Join(root, "ready"), []byte(action), 0600); err != nil {
			return err
		}
		for {
			if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
				return nil
			}
			time.Sleep(time.Millisecond * 5)
		}
	}
	switch action {
	case "prepare-crash":
		j.syncDirectory = func() error { return barrier() }
		err = j.Confirm(context.Background(), pending, func() time.Time { return time.Unix(99, 0) }, func(string) error { return errors.New("prepared confirmation stopped timer") })
	case "confirm-crash":
		err = j.Confirm(context.Background(), pending, func() time.Time { return time.Unix(99, 0) }, func(string) error { return barrier() })
	case "fire-crash":
		err = j.Fire(context.Background(), pending, func() time.Time { return time.Unix(100, 0) }, func(r JournalRecord) error {
			// Fake idempotent restoration and audit publication, scratch only.
			f, e := os.OpenFile(filepath.Join(root, "restore-marker"), os.O_CREATE|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			if _, e = f.WriteString(r.ID + " rules-and-enabled"); e != nil {
				f.Close()
				return e
			}
			if e = f.Sync(); e != nil {
				f.Close()
				return e
			}
			f.Close()
			return barrier()
		})
	case "fire":
		err = j.Fire(context.Background(), pending, func() time.Time { return time.Unix(100, 0) }, func(JournalRecord) error { return os.WriteFile(filepath.Join(root, "unexpected-restore"), nil, 0600) })
	case "confirm":
		err = j.Confirm(context.Background(), pending, func() time.Time { return time.Unix(99, 0) }, func(string) error { return os.WriteFile(filepath.Join(root, "unexpected-stop"), nil, 0600) })
	default:
		t.Fatal(action)
	}
	if err != nil {
		t.Fatal(err)
	}
}

type journalChild struct {
	cancel context.CancelFunc
	done   chan error
}

func startJournalChild(t *testing.T, root, action string) journalChild {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		out, se, _, e := (execpkg.Real{}).Run(ctx, exe, []string{"-test.run=^TestJournalScratchHelper$"}, execpkg.RunOpts{Env: append(os.Environ(), "JOURNAL_TEST_ROOT="+root, "JOURNAL_TEST_ACTION="+action)})
		if e != nil && ctx.Err() == nil {
			e = errors.New(string(out) + string(se))
		}
		done <- e
	}()
	t.Cleanup(cancel)
	return journalChild{cancel, done}
}
func waitJournalFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second * 10)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("helper did not reach boundary", path)
}
func waitJournalChild(t *testing.T, c journalChild, killed bool) {
	t.Helper()
	select {
	case err := <-c.done:
		if (err != nil) != killed {
			t.Fatal(err)
		}
	case <-time.After(time.Second * 10):
		t.Fatal("child stuck")
	}
}
func TestJournalCrossProcessKillAndCompetingDecision(t *testing.T) {
	for _, action := range []string{"confirm-crash", "fire-crash"} {
		t.Run(action, func(t *testing.T) {
			j, r := journalFixture(t)
			if err := j.Arm(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			first := startJournalChild(t, j.root, action)
			waitJournalFile(t, filepath.Join(j.root, "ready"))
			competing := "fire"
			if action == "fire-crash" {
				competing = "confirm"
			}
			second := startJournalChild(t, j.root, competing)
			waitJournalFile(t, filepath.Join(j.root, "started-"+competing))
			// The competing process must not complete while the first holds the stable lock.
			select {
			case err := <-second.done:
				t.Fatal("competing decision escaped lock", err)
			case <-time.After(time.Millisecond * 100):
			}
			first.cancel()
			waitJournalChild(t, first, true)
			if action == "confirm-crash" {
				waitJournalChild(t, second, false)
			} else {
				waitJournalChild(t, second, true)
			}
			reopened, err := OpenJournal(j.root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
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
			expected := 0
			if action == "fire-crash" {
				expected = 1
				waitJournalFile(t, filepath.Join(j.root, "restore-marker"))
			}
			if calls != expected {
				t.Fatal(calls, expected)
			}
			for _, leaf := range []string{"unexpected-stop", "unexpected-restore"} {
				if _, err = os.Stat(filepath.Join(j.root, leaf)); !os.IsNotExist(err) {
					t.Fatal(leaf, err)
				}
			}
		})
	}
}

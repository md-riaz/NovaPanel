package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type restoreRunner struct {
	calls [][]string
	check func()
	fail  bool
}

func (f *restoreRunner) Run(_ context.Context, p string, a []string, _ execpkg.RunOpts) ([]byte, []byte, int, error) {
	if f.check != nil {
		f.check()
	}
	f.calls = append(f.calls, append([]string{p}, a...))
	if f.fail {
		return nil, nil, 1, nil
	}
	return nil, nil, 0, nil
}
func restoreFixture(t *testing.T, enabled bool) (*ConfinedRestore, *VerifiedSnapshot, JournalRecord, string, *restoreRunner) {
	t.Helper()
	c, _ := custodyFixture(t)
	in := custodyInput()
	in.PriorEnabled = enabled
	digest, e := c.Put(context.Background(), custodyID, in)
	mustCustody(t, e)
	r := custodyRecord(digest)
	r.PriorEnabled = enabled
	s, e := c.Verify(context.Background(), r)
	mustCustody(t, e)
	t.Cleanup(func() { s.Close() })
	root := filepath.Join(custodyTempDir(t), "destination")
	mustCustody(t, os.Mkdir(root, 0700))
	for _, n := range []string{"ufw", "markers"} {
		mustCustody(t, os.Mkdir(filepath.Join(root, n), 0700))
	}
	f := &restoreRunner{}
	a, e := OpenConfinedRestore(root, f)
	mustCustody(t, e)
	t.Cleanup(func() { a.Close() })
	return a, s, r, root, f
}
func TestConfinedRestoreRejectsUnsafeStagingBeforeAnyWrite(t *testing.T) {
	a, s, r, root, f := restoreFixture(t, true)
	mustCustody(t, os.Symlink("/unopened", filepath.Join(root, "ufw", "user6.rules.next")))
	if a.Restore(context.Background(), r, s) == nil {
		t.Fatal("unsafe staging accepted")
	}
	if _, e := os.Stat(filepath.Join(root, "ufw", "after.rules")); !os.IsNotExist(e) {
		t.Fatal("partial effects before full staging admission", e)
	}
	if len(f.calls) != 0 {
		t.Fatal("command after write failure")
	}
}

func TestConfinedRestoreMarkerStagingAdmission(t *testing.T) {
	a, s, r, root, f := restoreFixture(t, true)
	mustCustody(t, os.Symlink("/unopened", filepath.Join(root, "markers", r.ID+".json.next")))
	if a.Restore(context.Background(), r, s) == nil {
		t.Fatal("unsafe marker staging accepted")
	}
	if len(f.calls) != 0 {
		t.Fatal("commands ran before marker staging admitted")
	}
}

func TestConfinedRestoreFailureReplay(t *testing.T) {
	for _, fault := range []string{"write", "command", "marker"} {
		t.Run(fault, func(t *testing.T) {
			a, s, r, root, f := restoreFixture(t, true)
			ctx := context.Background()
			j, jr := journalFixture(t)
			jr.SnapshotSHA256 = r.SnapshotSHA256
			mustCustody(t, j.Arm(ctx, jr))
			blocked := filepath.Join(root, "ufw", "after.rules.next")
			switch fault {
			case "write":
				mustCustody(t, os.Mkdir(blocked, 0700))
			case "command":
				f.fail = true
			case "marker":
				f.check = func() {
					mustCustody(t, os.Mkdir(filepath.Join(root, "markers", r.ID+".json.next"), 0700))
					f.check = nil
				}
			}
			callback := func(r JournalRecord) error { return a.Restore(ctx, r, s) }
			if j.Fire(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, callback) == nil {
				t.Fatal("fault accepted")
			}
			state, e := j.read()
			mustCustody(t, e)
			if state.State != "reverting" {
				t.Fatal(state.State)
			}
			if _, e := os.Stat(filepath.Join(root, "markers", r.ID+".json")); !os.IsNotExist(e) {
				t.Fatal("marker on failure", e)
			}
			if fault == "write" {
				mustCustody(t, os.Remove(blocked))
			}
			if fault == "marker" {
				mustCustody(t, os.Remove(filepath.Join(root, "markers", r.ID+".json.next")))
			}
			f.fail = false
			reopened, e := OpenJournal(j.root)
			mustCustody(t, e)
			defer reopened.Close()
			mustCustody(t, reopened.Fire(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, callback))
			n := len(f.calls)
			mustCustody(t, reopened.Fire(ctx, r.ID, func() time.Time { return time.Unix(102, 0) }, func(JournalRecord) error { t.Fatal("terminal replay"); return nil }))
			if len(f.calls) != n {
				t.Fatal("terminal commands")
			}
		})
	}
}

func TestConfinedRestoreDescriptorAndBinding(t *testing.T) {
	a, s, r, root, _ := restoreFixture(t, true)
	// Replace the source leaf via its retained parent; source path is never reopened.
	d := int(s.dirs[1].Fd())
	mustCustody(t, unix.Renameat(d, "user.rules", d, "user.rules.old"))
	fd, e := unix.Openat(d, "user.rules", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	mustCustody(t, e)
	replacement := os.NewFile(uintptr(fd), "replacement")
	_, e = replacement.WriteString("unverified replacement")
	mustCustody(t, e)
	mustCustody(t, replacement.Close())
	mustCustody(t, a.Restore(context.Background(), r, s))
	b, e := os.ReadFile(filepath.Join(root, "ufw", "user.rules"))
	mustCustody(t, e)
	if !bytes.Equal(b, custodyInput().Rules["user.rules"]) {
		t.Fatal("reopened source path")
	}
}

func TestConfinedRestoreRootAndPathReplacement(t *testing.T) {
	for _, which := range []string{"root", "ufw", "markers", "leaf"} {
		t.Run(which, func(t *testing.T) {
			a, s, r, root, f := restoreFixture(t, true)
			path := root
			if which != "root" {
				path = filepath.Join(root, which)
			}
			if which == "leaf" {
				mustCustody(t, os.Symlink("/unopened", filepath.Join(root, "ufw", "user.rules")))
			} else {
				mustCustody(t, os.Rename(path, path+".old"))
				mustCustody(t, os.Mkdir(path, 0700))
			}
			if a.Restore(context.Background(), r, s) == nil || len(f.calls) != 0 {
				t.Fatal("replacement accepted")
			}
		})
	}
}

func TestConfinedRestoreMatchingMarkerStillReplays(t *testing.T) {
	a, s, r, root, f := restoreFixture(t, true)
	ctx := context.Background()
	mustCustody(t, a.Restore(ctx, r, s))
	mustCustody(t, os.WriteFile(filepath.Join(root, "ufw", "user.rules"), []byte("changed"), 0600))
	mustCustody(t, a.Restore(ctx, r, s))
	if len(f.calls) != 4 {
		t.Fatal("matching marker skipped restore")
	}
	b, e := os.ReadFile(filepath.Join(root, "ufw", "user.rules"))
	mustCustody(t, e)
	if !bytes.Equal(b, custodyInput().Rules["user.rules"]) {
		t.Fatal("not restored")
	}
	for _, change := range []string{"digest", "enabled", "id"} {
		bad := r
		switch change {
		case "digest":
			bad.SnapshotSHA256 = strings.Repeat("0", 64)
		case "enabled":
			bad.PriorEnabled = false
		case "id":
			bad.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
		}
		if a.Restore(ctx, bad, s) == nil {
			t.Fatal("binding accepted", change)
		}
	}
	mustCustody(t, os.WriteFile(filepath.Join(root, "markers", r.ID+".json"), []byte("{}"), 0600))
	if a.Restore(ctx, r, s) == nil || len(f.calls) != 4 {
		t.Fatal("conflicting marker accepted")
	}
}

func TestConfinedRestoreLockCancellation(t *testing.T) {
	a, s, r, _, f := restoreFixture(t, true)
	lock, e := snapshotOpenDir(a.dir, ".")
	mustCustody(t, e)
	defer lock.Close()
	mustCustody(t, unix.Flock(int(lock.Fd()), unix.LOCK_EX))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if a.Restore(ctx, r, s) == nil || len(f.calls) != 0 {
		t.Fatal("lock bypass")
	}
}

func TestConfinedRestoreVerifiedJournalRetry(t *testing.T) {
	a, _, _, root, f := restoreFixture(t, true)
	c, _ := custodyFixture(t)
	ctx := context.Background()
	digest, e := c.Put(ctx, custodyID, custodyInput())
	mustCustody(t, e)
	j, r := journalFixture(t)
	r.SnapshotSHA256 = digest
	mustCustody(t, j.Arm(ctx, r))
	consume := func(record JournalRecord, s *VerifiedSnapshot) error { return a.Restore(ctx, record, s) }
	f.fail = true
	if j.RevertVerified(ctx, r.ID, func() time.Time { return time.Unix(99, 0) }, c, consume) == nil {
		t.Fatal("command failure consumed")
	}
	state, e := j.read()
	mustCustody(t, e)
	if state.State != "reverting" {
		t.Fatal(state.State)
	}
	f.fail = false
	reopened, e := OpenJournal(j.root)
	mustCustody(t, e)
	defer reopened.Close()
	mustCustody(t, reopened.RevertVerified(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, c, consume))
	b, e := os.ReadFile(filepath.Join(root, "markers", r.ID+".json"))
	mustCustody(t, e)
	if !bytes.Contains(b, []byte(digest)) {
		t.Fatal("wrong marker")
	}
	state, e = reopened.read()
	mustCustody(t, e)
	if state.State != "reverted" {
		t.Fatal(state.State)
	}
}

func TestConfinedRestoreReplacementDuringCommand(t *testing.T) {
	a, s, r, root, f := restoreFixture(t, true)
	f.check = func() {
		mustCustody(t, os.Rename(root, root+".old"))
		mustCustody(t, os.Mkdir(root, 0700))
		f.check = nil
	}
	if a.Restore(context.Background(), r, s) == nil || len(f.calls) != 1 {
		t.Fatal("postcommand root replacement accepted")
	}
	if _, e := os.Stat(filepath.Join(root+".old", "markers", r.ID+".json")); !os.IsNotExist(e) {
		t.Fatal("marked replaced root")
	}
}

func TestConfinedRestoreRejectsForgedInvalidID(t *testing.T) {
	a, s, r, _, f := restoreFixture(t, true)
	s.manifest.ID = "../escape"
	r.ID = s.manifest.ID
	b, e := json.Marshal(s.manifest)
	mustCustody(t, e)
	r.SnapshotSHA256 = snapshotDigest(b)
	if a.Restore(context.Background(), r, s) == nil || len(f.calls) != 0 {
		t.Fatal("invalid ID reached filesystem/commands")
	}
}

func TestConfinedRestoreRulesBeforeEnabledCommands(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			a, s, r, root, f := restoreFixture(t, enabled)
			f.check = func() {
				for n, want := range custodyInput().Rules {
					got, e := os.ReadFile(filepath.Join(root, "ufw", n))
					if e != nil || string(got) != string(want) {
						t.Fatalf("command before durable rules: %s %v", n, e)
					}
				}
			}
			mustCustody(t, a.Restore(context.Background(), r, s))
			want := [][]string{{"/usr/sbin/ufw", "disable"}}
			if enabled {
				want = [][]string{{"/usr/sbin/ufw", "--force", "enable"}, {"/usr/sbin/ufw", "reload"}}
			}
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatal(f.calls)
			}
			b, e := os.ReadFile(filepath.Join(root, "markers", r.ID+".json"))
			mustCustody(t, e)
			expected := `{"version":1,"id":"` + r.ID + `","snapshot_sha256":"` + r.SnapshotSHA256 + `","prior_enabled":` + map[bool]string{true: "true", false: "false"}[enabled] + `}`
			if string(b) != expected {
				t.Fatalf("marker %s", b)
			}
		})
	}
}

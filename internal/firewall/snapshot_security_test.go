package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSnapshotCustodyRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, string, *JournalRecord)
	}{
		{"missing", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.Remove(filepath.Join(p, "ufw/user6.rules")))
		}},
		{"truncated", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.Truncate(filepath.Join(p, "iptables-save"), 1))
		}},
		{"changed", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.WriteFile(filepath.Join(p, "iptables-save"), []byte("not capture"), 0600))
		}},
		{"swapped", func(t *testing.T, p string, r *JournalRecord) {
			a := filepath.Join(p, "ufw/after.rules")
			b := filepath.Join(p, "ufw/before.rules")
			mustCustody(t, os.Rename(a, a+".swap"))
			mustCustody(t, os.Rename(b, a))
			mustCustody(t, os.Rename(a+".swap", b))
		}},
		{"extra", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.WriteFile(filepath.Join(p, "extra"), nil, 0600))
		}},
		{"extra_rule", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.WriteFile(filepath.Join(p, "ufw/extra.rules"), nil, 0600))
		}},
		{"digest", func(t *testing.T, p string, r *JournalRecord) { r.SnapshotSHA256 = strings.Repeat("a", 64) }},
		{"enabled", func(t *testing.T, p string, r *JournalRecord) { r.PriorEnabled = false }},
		{"id", func(t *testing.T, p string, r *JournalRecord) {
			r.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
			mustCustody(t, os.Rename(p, filepath.Join(filepath.Dir(p), r.ID)))
		}},
		{"traversal", func(t *testing.T, p string, r *JournalRecord) { r.ID = "../" + custodyID }},
		{"file_symlink", func(t *testing.T, p string, r *JournalRecord) {
			f := filepath.Join(p, "iptables-save")
			mustCustody(t, os.Rename(f, f+".old"))
			mustCustody(t, os.Symlink("iptables-save.old", f))
		}},
		{"file_hardlink", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.Link(filepath.Join(p, "iptables-save"), filepath.Join(filepath.Dir(p), "linked")))
		}},
		{"weak_file", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.Chmod(filepath.Join(p, "iptables-save"), 0640))
		}},
		{"weak_snapshot", func(t *testing.T, p string, r *JournalRecord) { mustCustody(t, os.Chmod(p, 0750)) }},
		{"weak_ufw", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.Chmod(filepath.Join(p, "ufw"), 0750))
		}},
		{"directory_symlink", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.Rename(filepath.Join(p, "ufw"), filepath.Join(p, "ufw.old")))
			mustCustody(t, os.Symlink("ufw.old", filepath.Join(p, "ufw")))
		}},
		{"fifo", func(t *testing.T, p string, r *JournalRecord) {
			f := filepath.Join(p, "iptables-save")
			mustCustody(t, os.Remove(f))
			mustCustody(t, unix.Mkfifo(f, 0600))
		}},
		{"oversized_file", func(t *testing.T, p string, r *JournalRecord) {
			mustCustody(t, os.Truncate(filepath.Join(p, "iptables-save"), snapshotFileLimit+1))
		}},
		{"manifest_whitespace", func(t *testing.T, p string, r *JournalRecord) {
			f := filepath.Join(p, "manifest.json")
			b, e := os.ReadFile(f)
			mustCustody(t, e)
			b = append(b, '\n')
			mustCustody(t, os.WriteFile(f, b, 0600))
			r.SnapshotSHA256 = snapshotDigest(b)
		}},
		{"manifest_duplicate", func(t *testing.T, p string, r *JournalRecord) {
			f := filepath.Join(p, "manifest.json")
			b, e := os.ReadFile(f)
			mustCustody(t, e)
			b = bytes.Replace(b, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
			mustCustody(t, os.WriteFile(f, b, 0600))
			r.SnapshotSHA256 = snapshotDigest(b)
		}},
		{"manifest_order", func(t *testing.T, p string, r *JournalRecord) {
			rewriteCustodyManifest(t, p, r, func(m *snapshotManifest) { m.Artifacts[0], m.Artifacts[1] = m.Artifacts[1], m.Artifacts[0] })
		}},
		{"manifest_total", func(t *testing.T, p string, r *JournalRecord) {
			rewriteCustodyManifest(t, p, r, func(m *snapshotManifest) {
				for i := range m.Artifacts {
					m.Artifacts[i].Size = snapshotFileLimit
				}
			})
		}},
		{"manifest_negative", func(t *testing.T, p string, r *JournalRecord) {
			rewriteCustodyManifest(t, p, r, func(m *snapshotManifest) { m.Artifacts[0].Size = -1 })
		}},
		{"manifest_version", func(t *testing.T, p string, r *JournalRecord) {
			rewriteCustodyManifest(t, p, r, func(m *snapshotManifest) { m.Version = 2 })
		}},
		{"foreign_file", func(t *testing.T, p string, r *JournalRecord) {
			if os.Geteuid() != 0 {
				t.Skip("foreign owner requires root")
			}
			mustCustody(t, os.Chown(filepath.Join(p, "iptables-save"), 12345, 12345))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, root := custodyFixture(t)
			digest, e := c.Put(context.Background(), custodyID, custodyInput())
			mustCustody(t, e)
			r := custodyRecord(digest)
			tc.change(t, filepath.Join(root, custodyID), &r)
			s, e := c.Verify(context.Background(), r)
			if e == nil {
				s.Close()
				t.Fatal("accepted unsafe custody")
			}
			if e.Error() != errSnapshot.Error() {
				t.Fatal("non generic error")
			}
		})
	}
}
func mustCustody(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func rewriteCustodyManifest(t *testing.T, p string, r *JournalRecord, change func(*snapshotManifest)) {
	t.Helper()
	f := filepath.Join(p, "manifest.json")
	b, e := os.ReadFile(f)
	mustCustody(t, e)
	var m snapshotManifest
	mustCustody(t, json.Unmarshal(b, &m))
	change(&m)
	b, e = json.Marshal(m)
	mustCustody(t, e)
	mustCustody(t, os.WriteFile(f, b, 0600))
	r.SnapshotSHA256 = snapshotDigest(b)
}
func TestSnapshotCustodyInputBounds(t *testing.T) {
	for _, name := range []string{"id", "missing_v6", "missing_capture", "empty", "unknown", "per_file", "total"} {
		t.Run(name, func(t *testing.T) {
			c, root := custodyFixture(t)
			in := custodyInput()
			pending := custodyID
			switch name {
			case "id":
				pending = "../bad"
			case "missing_v6":
				delete(in.Rules, "user6.rules")
			case "missing_capture":
				in.IP6TablesSave = nil
			case "empty":
				in.Rules["user.rules"] = nil
			case "unknown":
				delete(in.Rules, "user.rules")
				in.Rules["unknown.rules"] = []byte("secret")
			case "per_file":
				in.IPTablesSave = make([]byte, snapshotFileLimit+1)
			case "total":
				for n := range in.Rules {
					in.Rules[n] = make([]byte, snapshotFileLimit)
				}
			}
			if d, e := c.Put(context.Background(), pending, in); e == nil || d != "" {
				t.Fatal("accepted invalid input")
			}
			entries, e := os.ReadDir(root)
			mustCustody(t, e)
			if len(entries) != 0 {
				t.Fatal("invalid input created artifacts")
			}
		})
	}
}
func TestSnapshotCustodyRootAdmission(t *testing.T) {
	for _, name := range []string{"absent", "relative", "weak", "symlink", "writable_ancestor", "foreign_ancestor", "replacement"} {
		t.Run(name, func(t *testing.T) {
			parent := custodyTempDir(t)
			root := filepath.Join(parent, "custody")
			mustCustody(t, os.Mkdir(root, 0700))
			switch name {
			case "absent":
				mustCustody(t, os.Remove(root))
			case "relative":
				root = "relative"
			case "weak":
				mustCustody(t, os.Chmod(root, 0755))
			case "symlink":
				mustCustody(t, os.Rename(root, root+".old"))
				mustCustody(t, os.Symlink(root+".old", root))
			case "writable_ancestor":
				mustCustody(t, os.Chmod(parent, 0777))
			case "foreign_ancestor":
				if os.Geteuid() != 0 {
					t.Skip("foreign owner requires root")
				}
				mustCustody(t, os.Chown(parent, 12345, 12345))
			}
			c, e := OpenSnapshotCustody(root)
			if name != "replacement" {
				if e == nil {
					c.Close()
					t.Fatal("unsafe root admitted")
				}
				return
			}
			mustCustody(t, e)
			defer c.Close()
			mustCustody(t, os.Rename(root, root+".old"))
			mustCustody(t, os.Mkdir(root, 0700))
			if _, e = c.Put(context.Background(), custodyID, custodyInput()); e == nil {
				t.Fatal("replaced root accepted")
			}
		})
	}
}
func TestSnapshotCustodyImmutableAndCopied(t *testing.T) {
	c, root := custodyFixture(t)
	in := custodyInput()
	original := bytes.Clone(in.IPTablesSave)
	changed := false
	c.syncFile = func(f *os.File) error {
		if !changed {
			changed = true
			copy(in.IPTablesSave, []byte("mutated!!!"))
		}
		return f.Sync()
	}
	d, e := c.Put(context.Background(), custodyID, in)
	mustCustody(t, e)
	c.syncFile = nil
	s, e := c.Verify(context.Background(), custodyRecord(d))
	mustCustody(t, e)
	got, e := io.ReadAll(s.files["iptables-save"])
	mustCustody(t, e)
	s.Close()
	if !bytes.Equal(got, original) {
		t.Fatal("input was not copied")
	}
	before, e := os.ReadFile(filepath.Join(root, custodyID, "manifest.json"))
	mustCustody(t, e)
	if _, e = c.Put(context.Background(), custodyID, in); e == nil {
		t.Fatal("different content accepted")
	}
	after, e := os.ReadFile(filepath.Join(root, custodyID, "manifest.json"))
	mustCustody(t, e)
	if !bytes.Equal(before, after) {
		t.Fatal("immutable manifest replaced")
	}
}
func TestSnapshotCustodyFaultReopen(t *testing.T) {
	// 11 files, ufw dir, snapshot dir, root dir, plus the atomic rename boundary.
	for boundary := 1; boundary <= 15; boundary++ {
		t.Run(string(rune('A'+boundary-1)), func(t *testing.T) {
			c, root := custodyFixture(t)
			calls := 0
			c.syncFile = func(f *os.File) error {
				calls++
				if calls == boundary {
					return unix.EIO
				}
				return f.Sync()
			}
			if boundary == 15 {
				c.rename = func(int, string, int, string) error { return unix.EIO }
			}
			d, e := c.Put(context.Background(), custodyID, custodyInput())
			if e == nil || d != "" {
				t.Fatal("fault accepted", boundary, calls)
			}
			other, e := OpenSnapshotCustody(root)
			mustCustody(t, e)
			defer other.Close()
			if boundary == 14 {
				// Rename cannot be undone. Exact complete replay establishes durability.
				d, e = other.Put(context.Background(), custodyID, custodyInput())
				mustCustody(t, e)
				s, e := other.Verify(context.Background(), custodyRecord(d))
				mustCustody(t, e)
				s.Close()
			} else {
				if _, e = other.Put(context.Background(), custodyID, custodyInput()); e == nil {
					t.Fatal("partial staging adopted")
				}
				if _, e = os.Stat(filepath.Join(root, custodyID)); !os.IsNotExist(e) {
					t.Fatal("published before boundary", e)
				}
			}
		})
	}
}
func TestSnapshotCustodyCancellationAndReplaySync(t *testing.T) {
	c, root := custodyFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.Put(ctx, custodyID, custodyInput()); e == nil {
		t.Fatal("canceled publication")
	}
	c.syncFile = func(f *os.File) error { e := f.Sync(); cancel(); return e }
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if _, e := c.Put(ctx, custodyID, custodyInput()); e == nil {
		t.Fatal("midpublication cancellation")
	}
	if _, e := os.Stat(filepath.Join(root, custodyID)); !os.IsNotExist(e) {
		t.Fatal("canceled final published")
	}
	c2, _ := custodyFixture(t)
	d, e := c2.Put(context.Background(), custodyID, custodyInput())
	mustCustody(t, e)
	c2.syncFile = func(*os.File) error { return unix.EIO }
	if _, e = c2.Put(context.Background(), custodyID, custodyInput()); e == nil {
		t.Fatal("unsynced replay accepted")
	}
	if s, e := c2.Verify(context.Background(), custodyRecord(d)); e == nil {
		s.Close()
		t.Fatal("unsynced verification accepted")
	}
}
func TestSnapshotCustodyConcurrentPublication(t *testing.T) {
	c, root := custodyFixture(t)
	other, e := OpenSnapshotCustody(root)
	mustCustody(t, e)
	defer other.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, writer := range []*SnapshotCustody{c, other} {
		wg.Add(1)
		go func(w *SnapshotCustody) {
			defer wg.Done()
			<-start
			_, e := w.Put(context.Background(), custodyID, custodyInput())
			errs <- e
		}(writer)
	}
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		}
	}
	if success < 1 {
		t.Fatal("no successful publisher")
	}
	d, e := other.Put(context.Background(), custodyID, custodyInput())
	mustCustody(t, e)
	s, e := other.Verify(context.Background(), custodyRecord(d))
	mustCustody(t, e)
	s.Close()
}
func TestSnapshotCustodyRootReplacedDuringFinalSync(t *testing.T) {
	c, root := custodyFixture(t)
	c.syncFile = func(f *os.File) error {
		e := f.Sync()
		if f == c.dir {
			mustCustody(t, os.Rename(root, root+".old"))
			mustCustody(t, os.Mkdir(root, 0700))
		}
		return e
	}
	if digest, e := c.Put(context.Background(), custodyID, custodyInput()); e == nil || digest != "" {
		t.Fatal("replaced root authorized publication")
	}
}

func TestSnapshotCustodyVerifiedDescriptorsDoNotReopen(t *testing.T) {
	c, root := custodyFixture(t)
	ctx := context.Background()
	in := custodyInput()
	digest, e := c.Put(ctx, custodyID, in)
	mustCustody(t, e)
	s, e := c.Verify(ctx, custodyRecord(digest))
	mustCustody(t, e)
	defer s.Close()
	p := filepath.Join(root, custodyID, "ufw/user.rules")
	mustCustody(t, os.Rename(p, p+".old"))
	mustCustody(t, os.WriteFile(p, []byte("unverified replacement"), 0600))
	got, e := io.ReadAll(s.files["ufw/user.rules"])
	mustCustody(t, e)
	if !bytes.Equal(got, in.Rules["user.rules"]) {
		t.Fatal("consumer reopened replaced path")
	}
	if next, e := c.Verify(ctx, custodyRecord(digest)); e == nil {
		next.Close()
		t.Fatal("later verification accepted replacement")
	}
}

func TestSnapshotCustodyPartialStagingNeverAdopted(t *testing.T) {
	c, root := custodyFixture(t)
	ctx := context.Background()
	d, e := c.Put(ctx, custodyID, custodyInput())
	mustCustody(t, e)
	mustCustody(t, os.Mkdir(filepath.Join(root, custodyID+".next"), 0700))
	if s, e := c.Verify(ctx, custodyRecord(d)); e == nil {
		s.Close()
		t.Fatal("partial staging accepted with final")
	}
	if _, e := c.Put(ctx, custodyID, custodyInput()); e == nil {
		t.Fatal("partial staging accepted on replay")
	}
}

func TestSnapshotCustodyNoReplaceAtRename(t *testing.T) {
	c, root := custodyFixture(t)
	c.rename = func(a int, from string, b int, to string) error {
		mustCustody(t, os.Mkdir(filepath.Join(root, to), 0700))
		mustCustody(t, os.WriteFile(filepath.Join(root, to, "sentinel"), []byte("existing immutable evidence"), 0600))
		return unix.Renameat2(a, from, b, to, unix.RENAME_NOREPLACE)
	}
	if _, e := c.Put(context.Background(), custodyID, custodyInput()); e == nil {
		t.Fatal("rename replaced occupied ID")
	}
	b, e := os.ReadFile(filepath.Join(root, custodyID, "sentinel"))
	mustCustody(t, e)
	if string(b) != "existing immutable evidence" {
		t.Fatal("existing ID replaced")
	}
}

func TestSnapshotCustodyDisabledRoundTrip(t *testing.T) {
	c, _ := custodyFixture(t)
	in := custodyInput()
	in.PriorEnabled = false
	ctx := context.Background()
	digest, e := c.Put(ctx, custodyID, in)
	mustCustody(t, e)
	r := custodyRecord(digest)
	r.PriorEnabled = false
	s, e := c.Verify(ctx, r)
	mustCustody(t, e)
	defer s.Close()
	b, e := io.ReadAll(s.files["enabled"])
	mustCustody(t, e)
	if string(b) != "false\n" {
		t.Fatal("disabled state lost")
	}
}

func TestSnapshotCustodyJournalRecovery(t *testing.T) {
	for _, manual := range []bool{true, false} {
		name := "fire"
		if manual {
			name = "revert"
		}
		t.Run(name, func(t *testing.T) {
			c, root := custodyFixture(t)
			ctx := context.Background()
			d, e := c.Put(ctx, custodyID, custodyInput())
			mustCustody(t, e)
			j, r := journalFixture(t)
			r.SnapshotSHA256 = d
			mustCustody(t, j.Arm(ctx, r))
			file := filepath.Join(root, custodyID, "ufw/user.rules")
			original, e := os.ReadFile(file)
			mustCustody(t, e)
			mustCustody(t, os.WriteFile(file, []byte("tampered"), 0600))
			target := filepath.Join(t.TempDir(), "fake-restored")
			calls := 0
			callback := func(r JournalRecord) error {
				s, e := c.Verify(ctx, r)
				if e != nil {
					return e
				}
				defer s.Close()
				calls++
				b, e := io.ReadAll(s.files["ufw/user.rules"])
				if e != nil {
					return e
				}
				return os.WriteFile(target, b, 0600)
			}
			now := func() time.Time { return time.Unix(99, 0) }
			if manual {
				e = j.Revert(ctx, r.ID, now, callback)
			} else {
				e = j.Fire(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, callback)
			}
			if e == nil || calls != 0 {
				t.Fatal("verification did not gate consumer")
			}
			state, e := j.read()
			mustCustody(t, e)
			if state.State != "reverting" {
				t.Fatal("lost reverting")
			}
			reopened, e := OpenJournal(j.root)
			mustCustody(t, e)
			defer reopened.Close()
			independent, e := OpenSnapshotCustody(root)
			mustCustody(t, e)
			defer independent.Close()
			c = independent
			mustCustody(t, os.WriteFile(file, original, 0600))
			if manual {
				e = reopened.Revert(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, callback)
			} else {
				e = reopened.Fire(ctx, r.ID, func() time.Time { return time.Unix(101, 0) }, callback)
			}
			mustCustody(t, e)
			got, e := os.ReadFile(target)
			mustCustody(t, e)
			if !bytes.Equal(got, original) || calls != 1 {
				t.Fatal("fake descriptor restoration mismatch")
			}
			mustCustody(t, reopened.Fire(ctx, r.ID, func() time.Time { return time.Unix(102, 0) }, func(JournalRecord) error { return errors.New("terminal consumer called") }))
		})
	}
}

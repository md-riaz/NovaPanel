package firewall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
)

func TestInstalledRestoreChildLock(t *testing.T) {
	if os.Getenv("INSTALLED_TEST_CHILD") == "1" {
		rules, private, croot := os.Getenv("INSTALLED_RULES"), os.Getenv("INSTALLED_PRIVATE"), os.Getenv("INSTALLED_CUSTODY")
		mustCustody(t, os.WriteFile(filepath.Join(private, "child-started"), nil, 0600))
		c, e := OpenSnapshotCustody(croot)
		mustCustody(t, e)
		defer c.Close()
		digest, e := c.Put(context.Background(), custodyID, custodyInput())
		mustCustody(t, e)
		r := custodyRecord(digest)
		s, e := c.Verify(context.Background(), r)
		mustCustody(t, e)
		defer s.Close()
		f := &restoreRunner{check: func() { mustCustody(t, os.WriteFile(filepath.Join(private, "child-inside"), nil, 0600)) }}
		a, e := openInstalledRestore(rules, private, uint32(os.Geteuid()), f)
		mustCustody(t, e)
		defer a.Close()
		mustCustody(t, a.Restore(context.Background(), r, s))
		return
	}
	a, s, r, rules, private, f := installedFixture(t, true)
	c, _ := custodyFixture(t)
	_, e := c.Put(context.Background(), custodyID, custodyInput())
	mustCustody(t, e)
	entered, release := make(chan struct{}), make(chan struct{})
	f.check = func() { f.check = nil; close(entered); <-release }
	done := make(chan error, 1)
	go func() { done <- a.Restore(context.Background(), r, s) }()
	<-entered
	exe, e := os.Executable()
	mustCustody(t, e)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := make(chan error, 1)
	go func() {
		_, _, _, e := (execpkg.Real{}).Run(ctx, exe, []string{"-test.run=^TestInstalledRestoreChildLock$"}, execpkg.RunOpts{Env: append(os.Environ(), "INSTALLED_TEST_CHILD=1", "INSTALLED_RULES="+rules, "INSTALLED_PRIVATE="+private, "INSTALLED_CUSTODY="+c.root)})
		child <- e
	}()
	waitJournalFile(t, filepath.Join(private, "child-started"))
	select {
	case e := <-child:
		t.Fatal("child escaped lock", e)
	case <-time.After(30 * time.Millisecond):
	}
	if _, e := os.Stat(filepath.Join(private, "child-inside")); !os.IsNotExist(e) {
		t.Fatal("child entered locked runner", e)
	}
	close(release)
	mustCustody(t, <-done)
	mustCustody(t, <-child)
	waitJournalFile(t, filepath.Join(private, "child-inside"))
}

func TestInstalledRestoreDescriptorCustody(t *testing.T) {
	for _, attack := range []string{"offset", "replace", "tamper", "id", "digest", "enabled"} {
		t.Run(attack, func(t *testing.T) {
			a, s, r, rules, _, f := installedFixture(t, true)
			switch attack {
			case "offset":
				for _, v := range s.files {
					_, e := v.Seek(2, 0)
					mustCustody(t, e)
				}
			case "replace":
				d := int(s.dirs[1].Fd())
				mustCustody(t, unix.Renameat(d, "user.rules", d, "user.rules.old"))
				fd, e := unix.Openat(d, "user.rules", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
				mustCustody(t, e)
				v := os.NewFile(uintptr(fd), "replacement")
				_, e = v.WriteString("unverified")
				mustCustody(t, e)
				mustCustody(t, v.Close())
			case "tamper":
				d := int(s.dirs[1].Fd())
				fd, e := unix.Openat(d, "user.rules", unix.O_WRONLY, 0)
				mustCustody(t, e)
				v := os.NewFile(uintptr(fd), "tamper")
				_, e = v.WriteAt([]byte("X"), 0)
				mustCustody(t, e)
				mustCustody(t, v.Close())
			case "id":
				r.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
			case "digest":
				r.SnapshotSHA256 = strings.Repeat("0", 64)
			case "enabled":
				r.PriorEnabled = false
			}
			e := a.Restore(context.Background(), r, s)
			ok := attack == "offset" || attack == "replace"
			if (e == nil) != ok {
				t.Fatal(e)
			}
			if ok {
				b, e := os.ReadFile(filepath.Join(rules, "user.rules"))
				mustCustody(t, e)
				if string(b) != string(custodyInput().Rules["user.rules"]) {
					t.Fatal("lost retained bytes")
				}
			} else if len(f.calls) != 0 {
				t.Fatal("binding effects")
			}
		})
	}
}
func TestInstalledRestoreMissingAndStaleStage(t *testing.T) {
	a, s, r, rules, _, _ := installedFixture(t, true)
	p := filepath.Join(rules, "user.rules")
	mustCustody(t, os.Remove(p))
	mustCustody(t, os.WriteFile(p+".novapanel-next", []byte("stale"), 0644))
	mustCustody(t, a.Restore(context.Background(), r, s))
	st, e := os.Stat(p)
	mustCustody(t, e)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	b, e := os.ReadFile(p)
	mustCustody(t, e)
	if string(b) != string(custodyInput().Rules["user.rules"]) {
		t.Fatal("adopted staging")
	}
}
func TestInstalledRestorePrivateAdmission(t *testing.T) {
	for _, attack := range []string{"root-mode", "markers-mode", "root-replaced", "markers-replaced", "ancestor-write", "ancestor-sticky", "marker-symlink", "stage-symlink", "marker-mode", "stage-mode"} {
		t.Run(attack, func(t *testing.T) {
			a, s, r, rules, private, f := installedFixture(t, true)
			switch attack {
			case "root-mode":
				mustCustody(t, os.Chmod(private, 0755))
			case "markers-mode":
				mustCustody(t, os.Chmod(filepath.Join(private, "markers"), 0755))
			case "root-replaced", "markers-replaced":
				p := private
				if attack == "markers-replaced" {
					p = filepath.Join(private, "markers")
				}
				mustCustody(t, os.Rename(p, p+".old"))
				mustCustody(t, os.Mkdir(p, 0700))
			case "ancestor-write", "ancestor-sticky":
				p := filepath.Dir(private)
				mode := os.FileMode(0777)
				if attack == "ancestor-sticky" {
					mode |= os.ModeSticky
				}
				mustCustody(t, os.Chmod(p, mode))
				defer os.Chmod(p, 0700)
			default:
				p := filepath.Join(private, "markers", r.ID+".json")
				if strings.HasPrefix(attack, "stage") {
					p += ".next"
				}
				if strings.HasSuffix(attack, "symlink") {
					mustCustody(t, os.Symlink("/unopened", p))
				} else {
					mustCustody(t, os.WriteFile(p, []byte("{}"), 0644))
				}
			}
			if a.Restore(context.Background(), r, s) == nil || len(f.calls) != 0 {
				t.Fatal("private admission failed")
			}
			b, e := os.ReadFile(filepath.Join(rules, "after.rules"))
			mustCustody(t, e)
			if string(b) != "old" {
				t.Fatal("early effect")
			}
		})
	}
}
func TestInstalledRestoreVerifiedJournalReplay(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "fire", true: "manual"}[manual], func(t *testing.T) {
			a, _, _, rules, private, f := installedFixture(t, true)
			ctx := context.Background()
			c, _ := custodyFixture(t)
			digest, e := c.Put(ctx, custodyID, custodyInput())
			mustCustody(t, e)
			j, r := journalFixture(t)
			r.SnapshotSHA256 = digest
			mustCustody(t, j.Arm(ctx, r))
			f.fail = true
			run := func(j *Journal, now int64) error {
				cb := func(r JournalRecord, s *VerifiedSnapshot) error { return a.Restore(ctx, r, s) }
				if manual {
					return j.RevertVerified(ctx, r.ID, func() time.Time { return time.Unix(now, 0) }, c, cb)
				}
				return j.FireVerified(ctx, r.ID, func() time.Time { return time.Unix(now, 0) }, c, cb)
			}
			now := int64(100)
			if manual {
				now = 99
			}
			if run(j, now) == nil {
				t.Fatal("command fault accepted")
			}
			st, e := j.read()
			mustCustody(t, e)
			if st.State != "reverting" {
				t.Fatal(st.State)
			}
			mustCustody(t, j.Close())
			j, e = OpenJournal(j.root)
			mustCustody(t, e)
			defer j.Close()
			root := c.root
			mustCustody(t, c.Close())
			c, e = OpenSnapshotCustody(root)
			mustCustody(t, e)
			defer c.Close()
			b, e := openInstalledRestore(rules, private, uint32(os.Geteuid()), f)
			mustCustody(t, e)
			defer b.Close()
			a = b
			f.fail = false
			mustCustody(t, run(j, 101))
			st, e = j.read()
			mustCustody(t, e)
			if st.State != "reverted" {
				t.Fatal(st.State)
			}
		})
	}
}
func TestInstalledRestoreFreshDescriptionLocks(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(map[bool]string{true: "same", false: "separate"}[same], func(t *testing.T) {
			a, s, r, rules, private, f := installedFixture(t, true)
			entered, release := make(chan struct{}), make(chan struct{})
			f.check = func() { f.check = nil; close(entered); <-release }
			done := make(chan error, 1)
			go func() { done <- a.Restore(context.Background(), r, s) }()
			<-entered
			b := a
			if !same {
				var e error
				b, e = openInstalledRestore(rules, private, uint32(os.Geteuid()), &restoreRunner{})
				mustCustody(t, e)
				defer b.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if b.Restore(ctx, r, s) == nil {
				t.Fatal("waiter bypassed lock")
			}
			close(release)
			mustCustody(t, <-done)
		})
	}
}

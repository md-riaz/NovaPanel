package firewall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func runtimeFixture(t *testing.T, enabled bool) (runtimeLayout, JournalRecord, *restoreRunner) {
	t.Helper()
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("runtime layout requires root UID/GID fixtures")
	}
	root := custodyTempDir(t)
	for _, n := range []string{"journal", "snapshots", "restore", "restore/markers", "rules"} {
		mustCustody(t, os.Mkdir(filepath.Join(root, n), 0700))
	}
	mustCustody(t, os.WriteFile(filepath.Join(root, "journal/lock"), nil, 0600))
	c, e := OpenSnapshotCustody(filepath.Join(root, "snapshots"))
	mustCustody(t, e)
	in := custodyInput()
	in.PriorEnabled = enabled
	digest, e := c.Put(context.Background(), custodyID, in)
	mustCustody(t, e)
	mustCustody(t, c.Close())
	r := custodyRecord(digest)
	r.State = "armed"
	r.Unit = "novapanel-fwrevert-" + r.ID
	r.Deadline = 100
	r.PriorEnabled = enabled
	j, e := OpenJournal(filepath.Join(root, "journal"))
	mustCustody(t, e)
	mustCustody(t, j.Arm(context.Background(), r))
	mustCustody(t, j.Close())
	for i, n := range snapshotNames[4:] {
		mustCustody(t, os.WriteFile(filepath.Join(root, "rules", strings.TrimPrefix(n, "ufw/")), []byte("old"), []os.FileMode{0600, 0640, 0644}[i%3]))
	}
	return runtimeLayout{root: root, rules: filepath.Join(root, "rules")}, r, &restoreRunner{}
}
func TestRuntimeRecoveryConnected(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			l, r, f := runtimeFixture(t, enabled)
			now := func() time.Time { return time.Unix(r.Deadline, 0) }
			mustCustody(t, recoverInstalled(context.Background(), r.ID, l, now, f))
			for i, n := range snapshotNames[4:] {
				leaf := strings.TrimPrefix(n, "ufw/")
				b, e := os.ReadFile(filepath.Join(l.rules, leaf))
				mustCustody(t, e)
				if !bytes.Equal(b, custodyInput().Rules[leaf]) {
					t.Fatal(leaf)
				}
				st, e := os.Stat(filepath.Join(l.rules, leaf))
				mustCustody(t, e)
				if st.Mode().Perm() != []os.FileMode{0600, 0640, 0644}[i%3] {
					t.Fatal("mode changed")
				}
			}
			marker, e := os.ReadFile(filepath.Join(l.root, "restore/markers", r.ID+".json"))
			mustCustody(t, e)
			if !bytes.Contains(marker, []byte(r.SnapshotSHA256)) || !bytes.Contains(marker, []byte(r.ID)) {
				t.Fatal("unbound marker")
			}
			j, e := OpenJournal(filepath.Join(l.root, "journal"))
			mustCustody(t, e)
			state, e := j.read()
			mustCustody(t, e)
			mustCustody(t, j.Close())
			if state.State != "reverted" {
				t.Fatal(state)
			}
			want := 1
			if enabled {
				want = 2
			}
			if len(f.calls) != want {
				t.Fatal(f.calls)
			}
			expected := [][]string{{"/usr/sbin/ufw", "disable"}}
			if enabled {
				expected = [][]string{{"/usr/sbin/ufw", "--force", "enable"}, {"/usr/sbin/ufw", "reload"}}
			}
			if !reflect.DeepEqual(f.calls, expected) {
				t.Fatal("unexpected enabled-state argv", f.calls)
			}
			// Terminal fire opens no installed destination and never runs commands again.
			mustCustody(t, os.Rename(l.rules, l.rules+".gone"))
			mustCustody(t, recoverInstalled(context.Background(), r.ID, l, now, f))
			if len(f.calls) != want {
				t.Fatal("terminal replay ran command")
			}
		})
	}
}

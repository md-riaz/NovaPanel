package firewall

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func installedFixture(t *testing.T, enabled bool) (*InstalledRestore, *VerifiedSnapshot, JournalRecord, string, string, *restoreRunner) {
	t.Helper()
	if os.Getegid() != 0 {
		t.Skip("installed destination policy requires GID 0 fixture creation")
	}
	_, s, r, _, f := restoreFixture(t, enabled)
	base := custodyTempDir(t)
	rules, private := filepath.Join(base, "installed"), filepath.Join(base, "private")
	mustCustody(t, os.Mkdir(rules, 0755))
	mustCustody(t, os.Mkdir(private, 0700))
	mustCustody(t, os.Mkdir(filepath.Join(private, "markers"), 0700))
	for i, n := range snapshotNames[4:] {
		mustCustody(t, os.WriteFile(filepath.Join(rules, strings.TrimPrefix(n, "ufw/")), []byte("old"), []os.FileMode{0600, 0640, 0644}[i%3]))
	}
	a, e := openInstalledRestore(rules, private, uint32(os.Geteuid()), f)
	mustCustody(t, e)
	t.Cleanup(func() { a.Close() })
	return a, s, r, rules, private, f
}
func TestInstalledRestoreEarlyAdmission(t *testing.T) {
	for _, n := range snapshotNames[4:] {
		for _, stage := range []bool{false, true} {
			for _, attack := range []string{"symlink", "link", "directory", "mode", "gid", "owner", "fifo"} {
				t.Run(strings.TrimPrefix(n, "ufw/")+map[bool]string{false: "-final-", true: "-stage-"}[stage]+attack, func(t *testing.T) {
					a, s, r, rules, private, f := installedFixture(t, true)
					if os.Geteuid() != 0 && (attack == "gid" || attack == "owner") {
						t.Skip("foreign ownership fixture requires root")
					}
					name := strings.TrimPrefix(n, "ufw/")
					if stage {
						name += ".novapanel-next"
					}
					p := filepath.Join(rules, name)
					if !stage {
						mustCustody(t, os.Remove(p))
					}
					switch attack {
					case "symlink":
						mustCustody(t, os.Symlink("/unopened", p))
					case "link":
						mustCustody(t, os.WriteFile(filepath.Join(rules, "extra-link-target"), []byte("old"), 0600))
						mustCustody(t, os.Link(filepath.Join(rules, "extra-link-target"), p))
					case "directory":
						mustCustody(t, os.Mkdir(p, 0700))
					case "fifo":
						mustCustody(t, unix.Mkfifo(p, 0600))
					default:
						mustCustody(t, os.WriteFile(p, []byte("old"), 0600))
						switch attack {
						case "mode":
							mustCustody(t, os.Chmod(p, 0660))
						case "gid":
							mustCustody(t, os.Chown(p, -1, 1))
						case "owner":
							mustCustody(t, os.Chown(p, 1, -1))
						}
					}
					if a.Restore(context.Background(), r, s) == nil {
						t.Fatal("unsafe leaf admitted")
					}
					if len(f.calls) != 0 {
						t.Fatal("commands before admission")
					}
					for _, other := range snapshotNames[4:] {
						leaf := strings.TrimPrefix(other, "ufw/")
						if !stage && leaf == strings.TrimPrefix(n, "ufw/") {
							continue
						}
						b, e := os.ReadFile(filepath.Join(rules, leaf))
						if e != nil || string(b) != "old" {
							t.Fatal("early rule effect", leaf, e)
						}
					}
					if _, e := os.Stat(filepath.Join(private, "markers", r.ID+".json")); !os.IsNotExist(e) {
						t.Fatal("early marker", e)
					}
				})
			}
		}
	}
}

func TestInstalledRestorePostCommand(t *testing.T) {
	for _, attack := range []string{"bytes", "mode", "link", "identical", "rules-root", "private-root", "markers"} {
		t.Run(attack, func(t *testing.T) {
			a, s, r, rules, private, f := installedFixture(t, true)
			f.check = func() {
				f.check = nil
				p := filepath.Join(rules, "user.rules")
				switch attack {
				case "bytes":
					mustCustody(t, os.WriteFile(p, []byte("changed"), 0600))
				case "mode":
					mustCustody(t, os.Chmod(p, 0666))
				case "link":
					mustCustody(t, os.Link(p, p+".link"))
				case "identical":
					b, e := os.ReadFile(p)
					mustCustody(t, e)
					st, e := os.Stat(p)
					mustCustody(t, e)
					mustCustody(t, os.Rename(p, p+".old"))
					mustCustody(t, os.WriteFile(p, b, st.Mode().Perm()))
				default:
					p = rules
					if attack == "private-root" {
						p = private
					}
					if attack == "markers" {
						p = filepath.Join(private, "markers")
					}
					mustCustody(t, os.Rename(p, p+".old"))
					mustCustody(t, os.Mkdir(p, 0700))
				}
			}
			e := a.Restore(context.Background(), r, s)
			if (e == nil) != (attack == "identical") {
				t.Fatal("command mutation result", e)
			}
			if e != nil {
				if _, x := os.Stat(filepath.Join(private, "markers", r.ID+".json")); !os.IsNotExist(x) {
					t.Fatal("marker on failure", x)
				}
			}
		})
	}
}
func TestInstalledRestoreMarkerReplay(t *testing.T) {
	a, s, r, rules, private, f := installedFixture(t, true)
	mustCustody(t, a.Restore(context.Background(), r, s))
	mustCustody(t, os.WriteFile(filepath.Join(rules, "user.rules"), []byte("changed"), 0600))
	mustCustody(t, a.Restore(context.Background(), r, s))
	if len(f.calls) != 4 {
		t.Fatal("marker skipped replay")
	}
	mustCustody(t, os.WriteFile(filepath.Join(private, "markers", r.ID+".json"), []byte("{}"), 0600))
	if a.Restore(context.Background(), r, s) == nil || len(f.calls) != 4 {
		t.Fatal("conflicting marker accepted")
	}
}
func TestInstalledRestoreLayout(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			a, s, r, rules, private, f := installedFixture(t, enabled)
			mustCustody(t, os.WriteFile(filepath.Join(rules, "extra.conf"), []byte("untouched"), 0644))
			f.check = func() {
				for n, want := range custodyInput().Rules {
					b, e := os.ReadFile(filepath.Join(rules, n))
					if e != nil || string(b) != string(want) {
						t.Fatal("command before rules", n, e)
					}
				}
			}
			mustCustody(t, a.Restore(context.Background(), r, s))
			for i, n := range snapshotNames[4:] {
				st, e := os.Stat(filepath.Join(rules, strings.TrimPrefix(n, "ufw/")))
				mustCustody(t, e)
				if st.Mode().Perm() != []os.FileMode{0600, 0640, 0644}[i%3] {
					t.Fatal(st.Mode())
				}
			}
			st, e := os.Stat(rules)
			mustCustody(t, e)
			if st.Mode().Perm() != 0755 {
				t.Fatal(st.Mode())
			}
			want := [][]string{{"/usr/sbin/ufw", "disable"}}
			if enabled {
				want = [][]string{{"/usr/sbin/ufw", "--force", "enable"}, {"/usr/sbin/ufw", "reload"}}
			}
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatal(f.calls)
			}
			b, e := os.ReadFile(filepath.Join(private, "markers", r.ID+".json"))
			mustCustody(t, e)
			if !strings.Contains(string(b), r.SnapshotSHA256) {
				t.Fatal(string(b))
			}
			if _, e := OpenConfinedRestore(rules, f); e == nil {
				t.Fatal("confined admission relaxed")
			}
		})
	}
}

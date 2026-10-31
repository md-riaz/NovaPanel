package wordpress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractVerifiedStageRestart(t *testing.T) {
	p, m := stageFixture(t)
	root := filepath.Join(t.TempDir(), "custody")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	b := StageBinding{JobID: "original-job", SiteID: "original-site", Docroot: "/srv/sites/example/public", UID: 23451, GID: 23452}
	r := &stagedRunner{t: t, status: validStatus(m)}
	run := func() error {
		return extractStaged(context.Background(), p, r, root, b, m, uint32(os.Getuid()), true, (*os.File).Sync)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, b.JobID, "tree", "index.php")
	first, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mode().Perm() != 0444 {
		t.Fatal("stage is writable")
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	again, _ := os.Stat(file)
	if !os.SameFile(first, again) {
		t.Fatal("restart replaced approved file")
	}
	if err := os.WriteFile(filepath.Join(root, b.JobID, "tree", "unmanaged.php"), []byte("untrusted"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("accepted unmanaged inventory")
	}
}
func TestExtractStageRefusesChangedCustody(t *testing.T) {
	for _, kind := range []string{"overlapping_root", "binding", "symlink", "hardlink", "bytes", "permissions", "site_owned", "root_replacement", "tree_replacement", "cancel", "cancel_after_sync", "sync_retry"} {
		t.Run(kind, func(t *testing.T) {
			p, m := stageFixture(t)
			root := filepath.Join(t.TempDir(), "custody")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			b := StageBinding{JobID: "original-job", SiteID: "original-site", Docroot: "/srv/sites/example/public", UID: 23451, GID: 23452}
			r := &stagedRunner{t: t, status: validStatus(m)}
			run := func(ctx context.Context, syncFile func(*os.File) error) error {
				return extractStaged(ctx, p, r, root, b, m, uint32(os.Getuid()), true, syncFile)
			}
			if err := run(context.Background(), (*os.File).Sync); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, b.JobID, "tree", "index.php")
			switch kind {
			case "overlapping_root":
				b.Docroot = root
				// Use a fresh binding so record mismatch cannot hide overlap.
				b.JobID = "overlap-job"
			case "binding":
				b.SiteID = "other-site"
			case "symlink":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(p.CLI, file); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(file, file+".link"); err != nil {
					t.Fatal(err)
				}
			case "bytes":
				before, err := os.Stat(file)
				if err != nil {
					t.Fatal(err)
				}
				// The fixture owns this read-only file. Temporarily allow writing
				// without relying on root's DAC override, then restore custody so
				// rejection exercises changed bytes, not changed permissions.
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("tampered"), 0444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(file, 0444); err != nil {
					t.Fatal(err)
				}
				after, err := os.Stat(file)
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(before, after) || after.Mode().Perm() != 0444 {
					t.Fatal("byte tampering changed inode or custody permissions")
				}
			case "site_owned":
				if os.Geteuid() != 0 {
					t.Skip("distinct UID requires root fixture")
				}
				if err := os.Chown(file, 23451, 23452); err != nil {
					t.Fatal(err)
				}
			case "permissions":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "root_replacement":
				changed := false
				err := run(context.Background(), func(f *os.File) error {
					if !changed {
						changed = true
						if err := os.Rename(root, root+".old"); err != nil {
							return err
						}
						if err := os.Mkdir(root, 0700); err != nil {
							return err
						}
					}
					return f.Sync()
				})
				if err == nil {
					t.Fatal("accepted relocated custody")
				}
				return
			case "tree_replacement":
				changed := false
				err := run(context.Background(), func(f *os.File) error {
					if f.Name() == "." && !changed {
						changed = true
						tree := filepath.Join(root, b.JobID, "tree")
						if err := os.Rename(tree, tree+".old"); err != nil {
							return err
						}
						if err := os.Mkdir(tree, 0700); err != nil {
							return err
						}
					}
					return f.Sync()
				})
				if err == nil {
					t.Fatal("accepted replaced tree")
				}
				return
			case "cancel":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				if err := run(ctx, (*os.File).Sync); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				return
			case "cancel_after_sync":
				ctx, cancel := context.WithCancel(context.Background())
				err := run(ctx, func(f *os.File) error { err := f.Sync(); cancel(); return err })
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if err := run(context.Background(), (*os.File).Sync); err != nil {
					t.Fatal(err)
				}
				return
			case "sync_retry":
				if err := run(context.Background(), func(*os.File) error { return errors.New("injected sync failure") }); err == nil {
					t.Fatal("ignored sync error")
				}
				if err := run(context.Background(), (*os.File).Sync); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err := run(context.Background(), (*os.File).Sync); err == nil {
				t.Fatal("accepted changed custody")
			}
		})
	}
}

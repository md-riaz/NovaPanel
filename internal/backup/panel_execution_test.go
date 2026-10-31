package backup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	runner "novapanel/internal/exec"
)

func TestSnapshotExecutionRequiresPanelAndLiveSnapshot(t *testing.T) {
	// No live DB or key is used by this invocation test.
	scratch := t.TempDir()
	os.Chmod(scratch, 0700)
	source := scratchDB(t)
	snap, err := CreateSnapshot(context.Background(), source, scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Close()
	p := &Password{plain: []byte("fixture-password")}
	defer p.Close()
	r := Repository{ID: "r", Name: "r", Role: "panel", Kind: "local", Location: "/srv/recovery"}
	f := &fakeRestic{}
	if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err != nil {
		t.Fatal(err)
	}
	want := []string{"-r", "/srv/recovery", "--retry-lock", "30s", "backup", "--tag", "panel", "--", snap.Path()}
	if !reflect.DeepEqual(f.args, want) {
		t.Fatal(f.args)
	}
	for _, code := range []int{3, 11, 12, 99} {
		f.code = code
		if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err == nil {
			t.Fatal("nonzero exit accepted", code)
		}
	}
	f.code = 0
	r.Role = "site"
	if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err == nil {
		t.Fatal("site repo accepted")
	}
	r.Role = "panel"
	snap.Close()
	if err = BackupPanelSnapshot(context.Background(), f, r, p, snap); err == nil {
		t.Fatal("closed snapshot accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = BackupPanelSnapshot(ctx, f, r, p, snap); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type inspectingRestic struct {
	t     *testing.T
	calls int
}

func (r *inspectingRestic) Run(ctx context.Context, binary string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if r.calls != 1 {
		r.t.Fatal("consumer invoked runner with changed path")
	}
	db, err := sql.Open("sqlite", "file:"+args[len(args)-1]+"?mode=ro")
	if err != nil {
		r.t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRowContext(ctx, "SELECT value FROM records WHERE id=1").Scan(&value); err != nil || value != "committed WAL row" {
		r.t.Fatalf("runner did not receive real snapshot: %q %v", value, err)
	}
	return nil, nil, 0, nil
}

func TestSnapshotConsumerRefusesChangedPathBeforeRunner(t *testing.T) {
	for _, change := range []string{"rename-root", "replace-leaf", "symlink-leaf", "wide-ancestor"} {
		t.Run(change, func(t *testing.T) {
			root := privateScratch(t)
			snap, err := CreateSnapshot(context.Background(), scratchDB(t), root)
			if err != nil {
				t.Fatal(err)
			}
			defer snap.Close()
			path := snap.Path()
			p := &Password{plain: []byte("fixture-password")}
			defer p.Close()
			repo := Repository{ID: "r", Name: "r", Role: "panel", Kind: "local", Location: "/srv/recovery"}
			run := &inspectingRestic{t: t}
			if err := BackupPanelSnapshot(context.Background(), run, repo, p, snap); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "rename-root":
				moved := root + "-moved"
				t.Cleanup(func() { os.RemoveAll(moved) })
				if err := os.Rename(root, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			case "replace-leaf", "symlink-leaf":
				moved := filepath.Join(filepath.Dir(path), "original")
				if err := os.Rename(path, moved); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Remove(moved) })
				if change == "replace-leaf" {
					err = os.WriteFile(path, []byte("substituted"), 0600)
				} else {
					err = os.Symlink(moved, path)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer os.Remove(moved)
			case "wide-ancestor":
				if err := os.Chmod(root, 0777); err != nil {
					t.Fatal(err)
				}
			}
			if err := BackupPanelSnapshot(context.Background(), run, repo, p, snap); err == nil {
				t.Fatal("changed path accepted")
			}
			if run.calls != 1 {
				t.Fatalf("unsafe runner invoked %d times", run.calls)
			}
			if snap.Path() != "" {
				t.Fatal("Path exposed changed identity")
			}
		})
	}
}

func TestRepositoryLocationsRejectInjection(t *testing.T) {
	r := Repository{ID: "r", Name: "r", Role: "panel"}
	for _, v := range []struct{ kind, location string }{
		{"local", "--password=secret"}, {"local", "/a/../b"}, {"local", "relative"},
		{"sftp", "sftp://user:password@host/repo"}, {"sftp", "sftp:host:/repo"},
		{"s3", "s3:http://host/bucket"}, {"s3", "s3:https://key:secret@host/bucket"},
		{"s3", "s3:https://host/bucket?token=secret"}, {"b2", "b2:secret"},
	} {
		r.Kind = v.kind
		r.Location = v.location
		if ValidateRepository(r) == nil {
			t.Fatal("accepted", v)
		}
	}
	for _, v := range []struct{ kind, location string }{{"local", "/srv/repo"}, {"sftp", "sftp://user@host/repo"}, {"s3", "s3:https://host/bucket/prefix"}} {
		r.Kind = v.kind
		r.Location = v.location
		if err := ValidateRepository(r); err != nil {
			t.Fatal(v, err)
		}
	}
}

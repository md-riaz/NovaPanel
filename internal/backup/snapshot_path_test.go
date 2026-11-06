package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

type pathSource struct {
	db     *sql.DB
	before func()
	calls  int
}

func (s *pathSource) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	s.calls++
	if s.before != nil {
		s.before()
	}
	return s.db.ExecContext(ctx, q, args...)
}

func TestSnapshotRootOwnedStickyAncestor(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root-owned sticky fixture")
	}
	parent := privateScratch(t)
	if err := os.Chmod(parent, os.ModeSticky|0777); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	snap, err := CreateSnapshot(context.Background(), scratchDB(t), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.Close(); err != nil {
		t.Fatal(err)
	}
	// Sticky protects child names, not a directory owned by an untrusted UID.
	foreign := filepath.Join(parent, "foreign")
	if err := os.Mkdir(foreign, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(foreign, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(foreign, "private")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := &pathSource{db: scratchDB(t)}
	if snap, err := CreateSnapshot(context.Background(), source, root); err == nil {
		snap.Close()
		t.Fatal("accepted foreign-owned descendant of sticky directory")
	}
	if source.calls != 0 {
		t.Fatal("unsafe descendant reached SQL")
	}
}

func TestSnapshotRefusesWritableAncestorBeforeSQL(t *testing.T) {
	for _, mode := range []os.FileMode{0777, 0770, 0720} {
		t.Run(mode.String(), func(t *testing.T) {
			parent := privateScratch(t)
			if err := os.Chmod(parent, mode); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(parent, "scratch")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			source := &pathSource{db: scratchDB(t)}
			snap, err := CreateSnapshot(context.Background(), source, root)
			if snap != nil {
				defer snap.Close()
			}
			if err == nil || source.calls != 0 {
				t.Fatalf("unsafe ancestor admitted: err=%v SQL calls=%d", err, source.calls)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("scratch changed: %v %v", entries, err)
			}
			st, err := os.Stat(parent)
			if err != nil || st.Mode().Perm() != mode {
				t.Fatalf("ancestor repaired: %v %v", st, err)
			}
		})
	}
}

func TestSnapshotRefusesPathRenamedDuringVacuum(t *testing.T) {
	root := privateScratch(t)
	moved := root + "-moved"
	t.Cleanup(func() { os.RemoveAll(moved) })
	source := &pathSource{db: scratchDB(t), before: func() {
		if err := os.Rename(root, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}}
	snap, err := CreateSnapshot(context.Background(), source, root)
	if snap != nil {
		defer snap.Close()
	}
	if err == nil {
		t.Fatal("returned success after trusted owner renamed scratch")
	}
	if source.calls != 1 {
		t.Fatal("real VACUUM not exercised")
	}
	entries, err := os.ReadDir(moved)
	if err != nil || len(entries) != 0 {
		t.Fatalf("held-directory cleanup failed: %v %v", entries, err)
	}
}

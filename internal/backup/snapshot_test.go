package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func scratchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", "CREATE TABLE records(id INTEGER PRIMARY KEY, value TEXT)", "INSERT INTO records VALUES(1, 'committed WAL row')"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func privateScratch(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSnapshotRefusesUnsafeRoots(t *testing.T) {
	db := scratchDB(t)
	safe := privateScratch(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(safe, link); err != nil {
		t.Fatal(err)
	}
	wide := t.TempDir()
	os.Chmod(wide, 0755)
	for _, path := range []string{link, filepath.Join(link, "child"), wide, "relative", safe + "/../" + filepath.Base(safe)} {
		t.Run(fmt.Sprintf("path-%d", len(path)), func(t *testing.T) {
			if s, err := CreateSnapshot(context.Background(), db, path); err == nil {
				s.Close()
				t.Fatal("accepted unsafe root")
			}
		})
	}
}

func TestSnapshotFailureCleansAndRedacts(t *testing.T) {
	root := privateScratch(t)
	db := scratchDB(t)
	db.Close()
	_, err := CreateSnapshot(context.Background(), db, root)
	if err == nil {
		t.Fatal("closed source accepted")
	}
	entries, e := os.ReadDir(root)
	if e != nil || len(entries) != 0 {
		t.Fatalf("left staging: %v %v", entries, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CreateSnapshot(ctx, scratchDB(t), root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	entries, _ = os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("cancellation left staging")
	}
	_, err = CreateSnapshot(context.Background(), leakingSource{}, root)
	if err == nil || strings.Contains(err.Error(), "secret-fixture") {
		t.Fatalf("leaked source error: %v", err)
	}
	entries, _ = os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatal("error left staging")
	}
}

type leakingSource struct{}

func (leakingSource) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, fmt.Errorf("secret-fixture")
}

func TestSnapshotConcurrentWAL(t *testing.T) {
	db := scratchDB(t)
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var committed atomic.Int64
	committed.Store(1)
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for i := int64(2); ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO records VALUES(?, ?)", i, "wal"); err != nil {
				done <- err
				return
			}
			committed.Store(i)
		}
	}()
	root := privateScratch(t)
	var last int64
	for i := 0; i < 6; i++ {
		before := committed.Load()
		s, err := CreateSnapshot(ctx, db, root)
		if err != nil {
			close(stop)
			<-done
			t.Fatal(err)
		}
		restored, err := sql.Open("sqlite", s.Path())
		if err != nil {
			t.Fatal(err)
		}
		var count, max int64
		var integrity string
		if err := restored.QueryRowContext(ctx, "SELECT count(*), max(id) FROM records").Scan(&count, &max); err != nil {
			t.Fatal(err)
		}
		if err := restored.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
			t.Fatal(err)
		}
		restored.Close()
		if integrity != "ok" || count != max || max < before || max < last {
			t.Fatalf("inconsistent snapshot count=%d max=%d before=%d last=%d integrity=%s", count, max, before, last, integrity)
		}
		last = max
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if committed.Load() <= 1 {
		t.Fatal("writer made no progress")
	}
}

func TestSnapshotIncludesUncheckpointedWAL(t *testing.T) {
	db := scratchDB(t)
	root := t.TempDir()
	var filename string
	var sequence int
	var dbName string
	if err := db.QueryRow("PRAGMA database_list").Scan(&sequence, &dbName, &filename); err != nil {
		t.Fatal(err)
	}
	wal, err := os.Stat(filename + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("missing WAL fixture: %v %v", wal, err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := CreateSnapshot(context.Background(), db, root)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	info, err := os.Stat(snapshot.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	parent, err := os.Stat(filepath.Dir(snapshot.Path()))
	if err != nil || parent.Mode().Perm() != 0700 {
		t.Fatalf("unsafe directory: %v %v", parent, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", snapshot.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var integrity, value string
	if err := restored.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity %q: %v", integrity, err)
	}
	if err := restored.QueryRow("SELECT value FROM records WHERE id=1").Scan(&value); err != nil || value != "committed WAL row" {
		t.Fatalf("row %q: %v", value, err)
	}
	restored.Close()
	path := snapshot.Path()
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("snapshot not removed: %v", err)
	}
}

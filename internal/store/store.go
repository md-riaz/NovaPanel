// Package store owns agent-only durable SQLite metadata.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*
var migrations embed.FS

type Store struct {
	db         *trackedDB
	clock      Clock
	operations map[*operation]struct{}
	pending    sync.WaitGroup
	lock       *os.File
	pidPath    string
	closeOnce  sync.Once
	closeErr   error
	mu         sync.Mutex
	closing    bool
}

func Open(path string) (*Store, error) { return OpenWithClock(path, realClock{}) }
func OpenWithClock(path string, clock Clock) (*Store, error) {
	if clock == nil {
		return nil, errors.New("nil clock")
	}
	var lock *os.File
	// Hold an advisory lifetime lock on the database inode, not a stale pidfile.
	// Production parents must be root-owned and non-writable by other users.
	if path != ":memory:" {
		fd, e := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
		if e != nil {
			return nil, e
		}
		lock = os.NewFile(uintptr(fd), path)
		var st unix.Stat_t
		if e = unix.Fstat(fd, &st); e == nil && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1) {
			e = errors.New("unsafe database file")
		}
		if e == nil {
			e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		}
		if e != nil {
			lock.Close()
			return nil, e
		}
	}
	release := func() {
		if lock != nil {
			lock.Close()
		}
	}

	// Driver pragmas apply to every connection, including pool replacements.
	// Escape literal filesystem paths so '?' and '#' cannot become URI options.
	dsn := ":memory:"
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			release()
			return nil, err
		}
		dsn = (&url.URL{Scheme: "file", Path: absolute}).String()
	}
	options := url.Values{"_pragma": {"busy_timeout(5000)", "foreign_keys(ON)", "journal_mode(WAL)"}}
	db, e := sql.Open("sqlite", dsn+"?"+options.Encode())
	if e != nil {
		release()
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{clock: clock, lock: lock, operations: make(map[*operation]struct{})}
	s.db = &trackedDB{raw: db, s: s}
	fail := func(e error) (*Store, error) { s.Close(); return nil, e }
	// Migration opens the first configured connection.
	if e = s.migrate(); e != nil {
		return fail(e)
	}
	if path != ":memory:" {
		if e = writePID(path + ".pid"); e != nil {
			return fail(e)
		}
		s.pidPath = path + ".pid"
	}
	return s, nil
}

// Close stops admission, cancels and drains all SQL work before unlocking.
// Callbacks must return on cancellation; do not call Close from a Tx callback.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		for op := range s.operations {
			op.cancel()
		}
		s.mu.Unlock()
		s.pending.Wait()
		s.closeErr = s.db.raw.Close()
		if s.pidPath != "" {
			s.closeErr = errors.Join(s.closeErr, os.Remove(s.pidPath))
		}
		if s.lock != nil {
			s.closeErr = errors.Join(s.closeErr, s.lock.Close())
		}
	})
	return s.closeErr
}
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	ctx, end, e := s.begin(ctx)
	if e != nil {
		return e
	}
	defer end()
	conn, e := s.db.raw.Conn(ctx)
	if e != nil {
		return e
	}
	defer conn.Close()
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		// SQLite may retain the transaction after a failed deferred-constraint
		// COMMIT, although database/sql has already marked Tx done. Clean up on
		// this pinned connection before returning it to the single-entry pool.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, rollbackErr := conn.ExecContext(cleanup, "ROLLBACK"); rollbackErr != nil {
			// An already-rolled-back transaction also takes this conservative
			// discard path. Never reuse a connection with uncertain SQL state.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		return e
	}
	return nil
}
func validateMigrationInventory(source fs.FS, entries []fs.DirEntry, hashes map[string]string) error {
	pattern := regexp.MustCompile(`^[0-9]{4}_[a-z][a-z0-9_]*\.sql$`)
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if name == "hashes.json" && !entry.IsDir() {
			continue
		}
		if entry.IsDir() || !pattern.MatchString(name) {
			return fmt.Errorf("migration inventory invalid filename %s", name)
		}
		version := name[:4]
		if version == "0000" || seen[version] {
			return fmt.Errorf("migration inventory duplicate or invalid version %s", version)
		}
		seen[version] = true
		b, e := fs.ReadFile(source, "migrations/"+name)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(b)
		if hashes[version] != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("migration inventory %s fixture hash mismatch", version)
		}
	}
	for version := range hashes {
		if !seen[version] {
			return fmt.Errorf("migration inventory orphan hash %s", version)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	entries, e := migrations.ReadDir("migrations")
	if e != nil {
		return e
	}
	raw, e := migrations.ReadFile("migrations/hashes.json")
	if e != nil {
		return e
	}
	var hashes map[string]string
	if e = json.Unmarshal(raw, &hashes); e != nil {
		return e
	}
	if e = validateMigrationInventory(migrations, entries, hashes); e != nil {
		return e
	}
	return s.Tx(context.Background(), func(tx *sql.Tx) error {
		var exists int
		if e := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&exists); e != nil {
			return e
		}
		known := map[string]bool{}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".sql") {
				continue
			}
			version := strings.SplitN(name, "_", 2)[0]
			known[version] = true
			b, e := migrations.ReadFile("migrations/" + name)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(b)
			hash := hex.EncodeToString(sum[:])
			if hashes[version] != hash {
				return fmt.Errorf("migration %s fixture hash mismatch", version)
			}
			if exists != 0 {
				var old string
				e = tx.QueryRow("SELECT hash FROM schema_migrations WHERE version=?", version).Scan(&old)
				if e == nil {
					if old != hash {
						return fmt.Errorf("migration %s database hash mismatch", version)
					}
					continue
				}
				if e != sql.ErrNoRows {
					return e
				}
			}
			if _, e = tx.Exec(string(b)); e != nil {
				return fmt.Errorf("migration %s: %w", version, e)
			}
			exists = 1
			if _, e = tx.Exec("INSERT INTO schema_migrations(version,hash,applied_at) VALUES(?,?,?)", version, hash, time.Now().UnixMilli()); e != nil {
				return e
			}
		}
		rows, e := tx.Query("SELECT version FROM schema_migrations")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if e = rows.Scan(&v); e != nil {
				return e
			}
			if !known[v] {
				return fmt.Errorf("unknown applied migration %s", v)
			}
		}
		return rows.Err()
	})
}

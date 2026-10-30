// Package store owns agent-only durable SQLite metadata.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*
var migrations embed.FS

type Store struct {
	db           *sql.DB
	lock         *os.File
	closeOnce    sync.Once
	closeErr     error
	mu           sync.Mutex
	closing      bool
	dbClosed     bool
	transactions map[*transaction]struct{}
}

type transaction struct{ cancel context.CancelFunc }

func Open(path string) (*Store, error) {
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
	s := &Store{db: db, lock: lock, transactions: make(map[*transaction]struct{})}
	fail := func(e error) (*Store, error) { s.Close(); return nil, e }
	// Migration opens the first configured connection.
	if e = s.migrate(); e != nil {
		return fail(e)
	}
	return s, nil
}

// Close rejects new work and cancels active transactions without waiting for
// arbitrary callback code. The lifetime lock stays held until every Tx callback
// has returned and its transaction has committed or rolled back. Callers must
// eventually return from callbacks, even when using context.Background().
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		for tx := range s.transactions {
			tx.cancel()
		}
		s.mu.Unlock()
		err := s.db.Close()
		s.mu.Lock()
		s.closeErr = errors.Join(s.closeErr, err)
		s.dbClosed = true
		s.releaseLock()
		s.mu.Unlock()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

// releaseLock requires mu. database/sql.Close alone does not drain transactions.
func (s *Store) releaseLock() {
	if s.dbClosed && len(s.transactions) == 0 && s.lock != nil {
		s.closeErr = errors.Join(s.closeErr, s.lock.Close())
		s.lock = nil
	}
}

func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	ctx, cancel := context.WithCancel(ctx)
	t := &transaction{cancel: cancel}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		cancel()
		return errors.New("store closed")
	}
	s.transactions[t] = struct{}{}
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.transactions, t)
		s.releaseLock()
		s.mu.Unlock()
	}()
	// Own the connection so Close also waits for database/sql's asynchronous
	// context-cancellation rollback before the lifetime lock can be released.
	conn, e := s.db.Conn(ctx)
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
	return tx.Commit()
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

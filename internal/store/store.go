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
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
	"os"
	"strings"
	"sync"
	"time"
)

//go:embed migrations/*
var migrations embed.FS

type Store struct {
	db        *sql.DB
	lock      *os.File
	closeOnce sync.Once
	closeErr  error
}

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

	db, e := sql.Open("sqlite", path)
	if e != nil {
		release()
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, lock: lock}
	fail := func(e error) (*Store, error) { s.Close(); return nil, e }
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, e = db.Exec(q); e != nil {
			return fail(e)
		}
	}
	if e = s.migrate(); e != nil {
		return fail(e)
	}
	return s, nil
}
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
		if s.lock != nil {
			s.closeErr = errors.Join(s.closeErr, s.lock.Close())
		}
	})
	return s.closeErr
}
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, e := s.db.BeginTx(ctx, nil)
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

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReplacementConnectionsKeepPragmas(t *testing.T) {
	// URI punctuation must remain part of the filename, not driver options.
	path := filepath.Join(t.TempDir(), "db?mode=memory#&.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.db.SetMaxIdleConns(0)
	for i := 0; i < 3; i++ {
		var fk, busy int
		if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if fk != 1 || busy != 5000 {
			t.Fatalf("connection %d: foreign_keys=%d busy_timeout=%d", i, fk, busy)
		}
		if err := s.AppendJobEvent(context.Background(), JobEvent{ID: "orphan", JobID: "missing", Msg: "no parent"}); err == nil {
			t.Fatal("replacement connection accepted orphan event")
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n); err != nil || n == 0 {
		t.Fatalf("migration persistence: %d, %v", n, err)
	}
}

func TestPublishClaimAtExpiryBoundary(t *testing.T) {
	for _, now := range []int64{99, 100, 101} {
		t.Run(fmt.Sprint(now), func(t *testing.T) {
			s := fresh(t)
			ctx := context.Background()
			if err := s.CreateJobWithClaim(ctx, Job{ID: "j", Verb: "v", Args: json.RawMessage(`{}`)}, JobClaim{JobID: "j", ClaimHash: "h", Expires: 100}); err != nil {
				t.Fatal(err)
			}
			err := s.PublishClaimAt(ctx, "j", []byte("n"), []byte("c"), now)
			if now < 100 {
				if err != nil {
					t.Fatal(err)
				}
				if err := s.PublishClaimAt(ctx, "j", []byte("n2"), []byte("c2"), now); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("republished: %v", err)
				}
			} else {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("expired: %v", err)
				}
				var size int
				if err := s.db.QueryRow("SELECT length(ciphertext) FROM job_claims WHERE job_id='j'").Scan(&size); err != nil || size != 0 {
					t.Fatalf("expired row modified: %d %v", size, err)
				}
			}
		})
	}
}

func TestPublishClaimRejectsExpired(t *testing.T) {
	s := fresh(t)
	ctx := context.Background()
	if err := s.CreateJobWithClaim(ctx, Job{ID: "expired", Verb: "v", Args: json.RawMessage(`{}`)}, JobClaim{JobID: "expired", ClaimHash: "h", Expires: time.Now().Add(-time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishClaim(ctx, "expired", []byte("n"), []byte("c")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired publication: %v", err)
	}
}

func TestCloseInsideTransactionDoesNotDeadlock(t *testing.T) {
	s := fresh(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.Tx(ctx, func(tx *sql.Tx) error {
			if err := s.Close(); err != nil {
				return err
			}
			return nil
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed transaction committed")
		}
	case <-ctx.Done():
		t.Fatal("Close deadlocked inside Tx")
	}
}

func TestConcurrentCloseAndTransactionAdmission(t *testing.T) {
	for round := 0; round < 10; round++ {
		p := filepath.Join(t.TempDir(), "db")
		s, err := Open(p)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_ = s.Tx(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO settings VALUES('race','true')")
					return err
				})
			}()
		}
		close(start)
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		wg.Wait()
		cancel()
		other, err := Open(p)
		if err != nil {
			t.Fatal("lock not drained:", err)
		}
		if err := other.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloseCancelsTransactionsAndKeepsLockUntilCallbacksReturn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.Exec("INSERT INTO settings VALUES('uncommitted','true')"); err != nil {
				return err
			}
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := tx.Exec("INSERT INTO settings VALUES('afterclose','true')")
			return err
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer close(release)
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Close blocked on callback")
	}
	if other, err := Open(p); err == nil {
		other.Close()
		t.Error("lifetime lock released with active callback")
	}
	if err := s.Tx(ctx, func(*sql.Tx) error { t.Error("callback after Close"); return nil }); err == nil {
		t.Error("new transaction accepted")
	}
	if err := s.SetSetting(ctx, "late", json.RawMessage(`true`)); err == nil {
		t.Error("write after Close accepted")
	}
	// Returning the callback must finish the canceled transaction and release the lock.
	release <- struct{}{}
	select {
	case err := <-done:
		if err == nil {
			t.Error("transaction committed after Close")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	other, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Setting(ctx, "uncommitted"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("canceled transaction persisted: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

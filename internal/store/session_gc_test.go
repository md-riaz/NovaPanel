package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionExpiryQueryPlan(t *testing.T) {
	s := fresh(t)
	rows, e := s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+sessionExpiryDelete, 1, 1, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
			t.Fatal(e)
		}
		plan = append(plan, detail)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	text := strings.Join(plan, "\n")
	for _, want := range []string{"COVERING INDEX idx_sessions_idle_deadline_id (idle_deadline<?)", "COVERING INDEX idx_sessions_abs_deadline_id (abs_deadline<?)", "SEARCH sessions USING INDEX sqlite_autoindex_sessions_1 (id=?)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in plan:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SCAN sessions") {
		t.Fatalf("unbounded sessions scan:\n%s", text)
	}
	t.Log(text)
}

func TestSessionExpiryMintRollback(t *testing.T) {
	for _, failure := range []string{"insert", "delete"} {
		t.Run(failure, func(t *testing.T) {
			c := &authClock{1800000000000}
			s, e := OpenWithClock(":memory:", c)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ctx := context.Background()
			old := gcSession("old", c.now)
			old.IdleDeadline = c.now + 1
			if e = s.CreateSession(ctx, old); e != nil {
				t.Fatal(e)
			}
			c.now += 2
			mint := gcSession("fresh", c.now)
			if failure == "insert" {
				mint.CSRFToken = ""
			} else {
				if _, e = s.db.Exec("CREATE TRIGGER refuse_gc BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT,'fixture refusal'); END"); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.CreateSession(ctx, mint); e == nil {
				t.Fatal("admitted mint after failure")
			}
			if gcCount(t, s, "id='old'") != 1 || gcCount(t, s, "id='fresh'") != 0 {
				t.Fatal("failed transaction changed sessions")
			}
		})
	}
}

type gcClock struct {
	now   atomic.Int64
	reads atomic.Int64
}

func (c *gcClock) Now() int64 { c.reads.Add(1); return c.now.Load() }

func TestSessionExpiryWaitClockAndCancellation(t *testing.T) {
	for _, mode := range []string{"pool", "sqlite", "cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c := &gcClock{}
			c.now.Store(1800000000000)
			path := filepath.Join(t.TempDir(), "panel.db")
			s, e := OpenWithClock(path, c)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			old := gcSession("old", c.now.Load())
			old.IdleDeadline = c.now.Load() + 1
			if e = s.CreateSession(ctx, old); e != nil {
				t.Fatal(e)
			}
			var release func()
			if mode == "sqlite" {
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.Exec("BEGIN IMMEDIATE"); err != nil {
					t.Fatal(err)
				}
				release = func() {
					if _, err = db.Exec("ROLLBACK"); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				conn, err := s.db.raw.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				release = func() { conn.Close() }
			}
			waitCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			reads := c.reads.Load()
			done := make(chan error, 1)
			go func() { done <- s.CreateSession(waitCtx, gcSession("fresh", 1800000000000)) }()
			deadline := time.After(2 * time.Second)
			for {
				s.mu.Lock()
				n := len(s.operations)
				s.mu.Unlock()
				if n > 0 {
					break
				}
				select {
				case <-deadline:
					t.Fatal("mint not admitted")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			// The actual database wait must not sample the clock before the write lock.
			time.Sleep(30 * time.Millisecond)
			if c.reads.Load() != reads {
				t.Error("clock sampled before connection/write lock")
			}
			c.now.Add(2)
			var closed chan error
			if mode == "cancel" {
				cancel()
			}
			if mode == "close" {
				closed = make(chan error, 1)
				go func() { closed <- s.Close() }()
			}
			if mode == "cancel" || mode == "close" {
				select {
				case err := <-done:
					if err == nil {
						t.Error("blocked mint succeeded")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("mint did not cancel")
				}
				release()
				if closed != nil {
					if e = <-closed; e != nil {
						t.Fatal(e)
					}
					if e = s.CreateSession(ctx, gcSession("later", c.now.Load())); !errors.Is(e, ErrStoreClosed) {
						t.Fatal(e)
					}
				} else if gcCount(t, s, "id='old'") != 1 {
					t.Fatal("cancelled mint deleted row")
				}
				return
			}
			release()
			select {
			case e = <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("mint stuck")
			}
			if gcCount(t, s, "id='old'") != 0 {
				t.Fatal("sweep used stale pre-wait clock")
			}
		})
	}
}

func gcSession(id string, now int64) Session {
	return Session{ID: id, TokenHash: "fixture-hash", Kind: "anon", CSRFToken: "fixture-csrf", IdleDeadline: now + 10000, AbsDeadline: now + 10000}
}

func gcCount(t *testing.T, s *Store, where string, args ...any) int {
	t.Helper()
	var n int
	if e := s.db.QueryRowContext(context.Background(), "SELECT count(*) FROM sessions WHERE "+where, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestAnonymousMintExpiryBatchesPersist(t *testing.T) {
	ctx := context.Background()
	c := &authClock{1800000000000}
	path := filepath.Join(t.TempDir(), "panel.db")
	s, e := OpenWithClock(path, c)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	// Raw fixtures also exercise token rows and independently expired absolute deadlines.
	if e = s.Tx(ctx, func(tx *sql.Tx) error {
		for i := 0; i < 701; i++ {
			idle, absolute := c.now+10000, c.now+10000
			switch i % 3 {
			case 0:
				idle = c.now - 1
			case 1:
				absolute = c.now - 1
			case 2:
				idle = c.now - 1
				absolute = c.now - 1
			}
			kind := []string{"anon", "user", "token"}[i%3]
			if _, err := tx.ExecContext(ctx, "INSERT INTO sessions("+sessionColumns+") VALUES(?,?,?,NULL,NULL,?,NULL,0,0,?,?)", fmt.Sprintf("expired-%04d", i), "fixture-hash", kind, "fixture-csrf", idle, absolute); err != nil {
				return err
			}
		}
		for _, id := range []string{"live", "idle-boundary", "absolute-boundary"} {
			idle, absolute := c.now+10000, c.now+10000
			if id == "idle-boundary" {
				idle = c.now
			}
			if id == "absolute-boundary" {
				absolute = c.now
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO sessions("+sessionColumns+") VALUES(?,?,'anon',NULL,NULL,?,NULL,0,0,?,?)", id, "fixture-hash", "fixture-csrf", idle, absolute); err != nil {
				return err
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for batch, want := range []int{445, 189, 0} {
		if e = s.CreateSession(ctx, gcSession(fmt.Sprintf("mint-%d", batch), c.now)); e != nil {
			t.Fatal(e)
		}
		if got := gcCount(t, s, "id LIKE 'expired-%'"); got != want {
			t.Fatalf("batch %d remaining=%d want=%d", batch, got, want)
		}
		if got := gcCount(t, s, "id IN ('live','idle-boundary','absolute-boundary')"); got != 3 {
			t.Fatalf("protected rows=%d", got)
		}
		if batch == 0 {
			// Equal deadline candidates are selected by id, then the deduplicated union is id limited.
			if got := gcCount(t, s, "id >= 'expired-0000' AND id <= 'expired-0255'"); got != 0 {
				t.Fatalf("non-deterministic first batch: %d", got)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			s, e = OpenWithClock(path, c)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	for _, id := range []string{"idle-boundary", "absolute-boundary"} {
		if _, e = s.GetSession(ctx, id); !errors.Is(e, ErrAuthExpired) {
			t.Fatalf("authentication boundary: %v", e)
		}
	}
	if _, e = s.GetSession(ctx, "live"); e != nil {
		t.Fatal(e)
	}
}

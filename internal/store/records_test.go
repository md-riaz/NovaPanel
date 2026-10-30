package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func fresh(t *testing.T) *Store {
	t.Helper()
	s, e := Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type testClock struct{ now atomic.Int64 }

func (c *testClock) Now() int64 { return c.now.Load() }
func TestPublicationClockAndOwnerOneShot(t *testing.T) {
	c := &testClock{}
	c.now.Store(10)
	s, e := OpenWithClock(filepath.Join(t.TempDir(), "db"), c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if e = s.CreateJobWithClaim(ctx, Job{ID: "j", Verb: "v", Args: json.RawMessage(`{}`), CreatedBy: "session"}, JobClaim{JobID: "j", ClaimHash: "h", Expires: 1}); e != nil {
		t.Fatal(e)
	}
	c.now.Store(1000000)
	if e = s.PublishClaim(ctx, "j", []byte("n"), []byte("c")); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeClaim(ctx, "j", "h", "other"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	got, e := s.ConsumeClaim(ctx, "j", "h", "session")
	if e != nil || got.Expires != 1000000+ClaimTTLMillis {
		t.Fatal(got, e)
	}
	if _, e = s.ConsumeClaim(ctx, "j", "h", "session"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
}
func TestCloseDrainsTransactionBeforeReleasingLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Tx(context.Background(), func(tx *sql.Tx) error {
			close(entered)
			<-release
			_, e := tx.Exec("INSERT INTO settings VALUES('x','true')")
			return e
		})
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("did not close")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case <-closed:
		t.Fatal("Close returned before callback drained")
	default:
	}
	if other, e := Open(p); e == nil {
		other.Close()
		t.Fatal("released active lock")
	}
	if e = s.SetSetting(context.Background(), "late", json.RawMessage(`true`)); !errors.Is(e, ErrStoreClosed) {
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e == nil {
		t.Fatal("committed after close")
	}
	if e = <-closed; e != nil {
		t.Fatal(e)
	}
	other, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	other.Close()
}

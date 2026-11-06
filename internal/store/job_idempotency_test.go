package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestReplayOwnerExpiryAndConflict(t *testing.T) {
	c := &testClock{}
	c.now.Store(1000)
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "db"), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	j := Job{ID: "job", Verb: "test", Args: []byte(`{}`), CreatedBy: "owner"}
	if _, _, _, err = s.AdmitJob(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, "key", "payload", []byte("sealed"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.ReplayJob(ctx, "other", "key", "payload"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owner: %v", err)
	}
	if _, _, _, err = s.ReplayJob(ctx, "owner", "key", "different"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if err = s.PublishClaim(ctx, "job", []byte("n"), []byte("cipher")); err != nil {
		t.Fatal(err)
	}
	c.now.Store(1000 + ClaimTTLMillis)
	got, token, found, err := s.ReplayJob(ctx, "owner", "key", "payload")
	if err != nil || !found || got.ID != j.ID || len(token) != 0 {
		t.Fatalf("expiry: job=%s token=%q found=%v err=%v", got.ID, token, found, err)
	}
	var saved sql.NullString
	if err = s.db.QueryRowContext(ctx, "SELECT result_json FROM idempotency WHERE id='key'").Scan(&saved); err != nil || saved.Valid {
		t.Fatalf("expired token retained: %v %v", saved, err)
	}
	c.now.Store(1000 + 24*60*60*1000 - 1)
	if got, _, found, err = s.ReplayJob(ctx, "owner", "key", "payload"); err != nil || !found || got.ID != j.ID {
		t.Fatalf("before 24h: found=%v err=%v", found, err)
	}
	c.now.Store(1000 + 24*60*60*1000)
	if _, _, found, err = s.ReplayJob(ctx, "owner", "key", "payload"); err != nil || found {
		t.Fatalf("24h expiry: found=%v err=%v", found, err)
	}
	fresh := Job{ID: "fresh", Verb: "test", Args: []byte(`{}`), CreatedBy: "owner"}
	if _, _, replay, err := s.AdmitJob(ctx, fresh, JobClaim{JobID: fresh.ID, ClaimHash: "new"}, "key", "payload", []byte("new"), nil, nil); err != nil || replay {
		t.Fatalf("new admission after 24h: replay=%v err=%v", replay, err)
	}
}

func TestFailedPublishedClaimReplay(t *testing.T) {
	for _, end := range []string{"consume", "expire"} {
		t.Run(end, func(t *testing.T) {
			c := &testClock{}
			c.now.Store(1000)
			s, err := OpenWithClock(filepath.Join(t.TempDir(), "db"), c)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ctx := context.Background()
			j := Job{ID: "job", Verb: "test", Args: []byte(`{}`), CreatedBy: "owner"}
			if _, _, _, err = s.AdmitJob(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, "key", "payload", []byte("sealed"), nil, nil); err != nil {
				t.Fatal(err)
			}
			if err = s.PublishClaim(ctx, j.ID, []byte("nonce"), []byte("cipher")); err != nil {
				t.Fatal(err)
			}
			j.State, j.Code = "failed", "agent_error"
			if err = s.UpdateJob(ctx, j); err != nil {
				t.Fatal(err)
			}
			got, token, found, err := s.ReplayJob(ctx, "owner", "key", "payload")
			if err != nil || !found || got.ID != j.ID || len(token) != 0 {
				t.Fatalf("live replay: job=%s token=%q found=%v err=%v", got.ID, token, found, err)
			}
			if _, _, _, err = s.ReplayJob(ctx, "other", "key", "payload"); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("owner: %v", err)
			}
			if _, _, _, err = s.ReplayJob(ctx, "owner", "key", "different"); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("payload: %v", err)
			}
			if end == "consume" {
				if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "owner"); err != nil {
					t.Fatal(err)
				}
			} else {
				c.now.Store(1000 + ClaimTTLMillis)
			}
			got, token, found, err = s.ReplayJob(ctx, "owner", "key", "payload")
			if err != nil || !found || got.ID != j.ID || len(token) != 0 {
				t.Fatalf("unavailable replay: job=%s token=%q found=%v err=%v", got.ID, token, found, err)
			}
			var saved sql.NullString
			if err = s.db.QueryRowContext(ctx, "SELECT result_json FROM idempotency WHERE id='key'").Scan(&saved); err != nil || saved.Valid {
				t.Fatalf("token retained: %v %v", saved, err)
			}
		})
	}
}

func TestIdempotencyAtomicRollbackAndConcurrency(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	job := func(id string) Job {
		return Job{ID: id, Verb: "test", Args: []byte(`{}`), ObjectKey: "x", CreatedBy: "owner", Created: 1, Updated: 1}
	}
	// A failed intent insert must roll back job, claim, and mapping.
	_, _, _, err = s.AdmitJob(ctx, job("failed"), JobClaim{JobID: "failed", ClaimHash: "hash"}, "key", "payload", []byte("sealed"), []byte{1}, nil)
	if err == nil {
		t.Fatal("invalid intent accepted")
	}
	if _, err = s.GetJob(ctx, "failed"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("job not rolled back: %v", err)
	}
	var n int
	for _, table := range []string{"job_claims", "idempotency", "secrets"} {
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s count=%d err=%v", table, n, err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			j, _, replay, e := s.AdmitJob(ctx, job(id), JobClaim{JobID: id, ClaimHash: "hash"}, "key", "payload", []byte("sealed"), nil, nil)
			if e != nil {
				errs <- e
				return
			}
			if replay {
				ids <- j.ID
			} else {
				ids <- id
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		t.Error(e)
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatal("multiple admissions")
		}
	}
	if err = s.db.QueryRow("SELECT count(*) FROM jobs").Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs=%d err=%v", n, err)
	}
}

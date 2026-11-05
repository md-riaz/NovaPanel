package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestExpiredClaimAttemptClearsOnlyMatchingToken(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{}
	clock.now.Store(1000)
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"expired", "live", "pending"} {
		j := Job{ID: id, Verb: "test", Args: []byte(`{}`), CreatedBy: "owner"}
		if _, _, _, err := s.AdmitJob(ctx, j, JobClaim{JobID: id, ClaimHash: "hash"}, id, "payload", []byte("sealed"), nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PublishClaim(ctx, "expired", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	clock.now.Store(1001)
	if err := s.PublishClaim(ctx, "live", []byte("n"), []byte("c")); err != nil {
		t.Fatal(err)
	}
	clock.now.Store(1000 + ClaimTTLMillis)
	check := func(id string, want bool) {
		t.Helper()
		var value sql.NullString
		var count int
		if err := s.db.QueryRowContext(ctx, "SELECT result_json FROM idempotency WHERE id=?", id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM job_claims WHERE job_id=?", id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if value.Valid != want || (count == 1) != want {
			t.Fatalf("%s: token retained=%v claim count=%d, want retained=%v", id, value.Valid, count, want)
		}
	}
	for _, attempt := range []struct{ hash, owner string }{{"wrong", "owner"}, {"hash", "wrong"}, {"wrong", "wrong"}} {
		if _, err := s.ConsumeClaim(ctx, "expired", attempt.hash, attempt.owner); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("unauthorized expired claim: %v", err)
		}
		check("expired", true)
	}
	if _, err := s.ConsumeClaim(ctx, "expired", "hash", "owner"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("authorized expired claim: %v", err)
	}
	check("expired", false)
	check("live", true)
	check("pending", true)
	if err := s.DeleteExpiredClaims(ctx); err != nil {
		t.Fatal(err)
	}
	check("live", true)
	check("pending", true)
	if _, err := s.ConsumeClaim(ctx, "live", "hash", "owner"); err != nil {
		t.Fatalf("live claim: %v", err)
	}
}

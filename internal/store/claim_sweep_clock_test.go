package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

type sweepAdvancingClock struct{ now int64 }

func (c *sweepAdvancingClock) Now() int64 {
	now := c.now
	c.now++
	return now
}

func TestClaimSweepUsesOneExpiryCutoff(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{}
	clock.now.Store(1000)
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j := Job{ID: "job", Verb: "test", Args: []byte(`{}`), CreatedBy: "owner"}
	if _, _, _, err := s.AdmitJob(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, "key", "payload", []byte("sealed"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishClaim(ctx, j.ID, []byte("nonce"), []byte("cipher")); err != nil {
		t.Fatal(err)
	}
	// The claim expires between the two clock reads in the broken sweep.
	s.clock = &sweepAdvancingClock{now: 1000 + ClaimTTLMillis - 1}
	if err := s.DeleteExpiredClaims(ctx); err != nil {
		t.Fatal(err)
	}
	check := func(wantClaim, wantToken bool) {
		t.Helper()
		var count int
		var token sql.NullString
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM job_claims WHERE job_id=?", j.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRowContext(ctx, "SELECT result_json FROM idempotency WHERE id=?", "key").Scan(&token); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != wantClaim || token.Valid != wantToken {
			t.Fatalf("claim present=%v token present=%v; want %v, %v", count == 1, token.Valid, wantClaim, wantToken)
		}
	}
	check(true, true)
	if err := s.DeleteExpiredClaims(ctx); err != nil {
		t.Fatal(err)
	}
	check(false, false)
}

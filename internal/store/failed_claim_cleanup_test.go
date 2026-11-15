package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestFailedJobClearsOnlyUnpublishedReplayToken(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{}
	clock.now.Store(1000)
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		id, state, code   string
		published, retain bool
	}{
		{"failed", "failed", "precondition_failed", false, false},
		{"interrupted", "failed", "interrupted", false, true},
		{"queued", "queued", "", false, true},
		{"done", "done", "", false, true},
		{"live", "failed", "agent_error", true, true},
	} {
		j := Job{ID: tc.id, Verb: "test", Args: []byte(`{}`), CreatedBy: "owner"}
		if _, _, _, err := s.AdmitJob(ctx, j, JobClaim{JobID: tc.id, ClaimHash: "hash"}, tc.id, "payload", []byte("sealed"), nil, nil); err != nil {
			t.Fatal(err)
		}
		if tc.published {
			if err := s.PublishClaim(ctx, tc.id, []byte("nonce"), []byte("ciphertext")); err != nil {
				t.Fatal(err)
			}
		}
		j.State, j.Code = tc.state, tc.code
		if err := s.UpdateJob(ctx, j); err != nil {
			t.Fatal(err)
		}
		var value sql.NullString
		if err := s.db.QueryRowContext(ctx, "SELECT result_json FROM idempotency WHERE id=?", tc.id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value.Valid != tc.retain {
			t.Errorf("%s token retained=%v, want %v", tc.id, value.Valid, tc.retain)
		}
	}
}

func TestFailedJobTokenCleanupRollsBackWithFinalization(t *testing.T) {
	ctx := context.Background()
	s, err := OpenWithClock(filepath.Join(t.TempDir(), "db"), &testClock{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	j := Job{ID: "job", Verb: "test", Args: []byte(`{}`), CreatedBy: "owner"}
	if _, _, _, err := s.AdmitJob(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}, "key", "payload", []byte("sealed"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_token_cleanup BEFORE UPDATE OF result_json ON idempotency BEGIN SELECT RAISE(ABORT, 'cleanup failed'); END`); err != nil {
		t.Fatal(err)
	}
	j.State, j.Code = "failed", "agent_error"
	if err := s.UpdateJob(ctx, j); err == nil {
		t.Fatal("expected cleanup failure")
	}
	got, err := s.GetJob(ctx, j.ID)
	if err != nil || got.State != "queued" {
		t.Fatalf("finalization not rolled back: state=%s err=%v", got.State, err)
	}
	var value sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT result_json FROM idempotency WHERE id='key'").Scan(&value); err != nil || !value.Valid {
		t.Fatalf("token lost: %v %v", value, err)
	}
}

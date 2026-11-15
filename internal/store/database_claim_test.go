package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
)

func TestDatabaseClaimCommitIsAtomicAndReplaySafe(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	j := Job{ID: "job", Verb: "database.create", ObjectKey: "db:db-a", CreatedBy: "owner", Role: "admin", Actor: "admin", State: "queued", Args: json.RawMessage(`{"db_id":"db-a","user_id":"user-a","name":"sample","username":"sample_user"}`)}
	if err = s.CreateJobWithClaim(ctx, j, JobClaim{JobID: j.ID, ClaimHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, j.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `DELETE FROM job_claims WHERE job_id=?`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitDatabaseClaim(ctx, j.ID, j.Verb, j.ObjectKey, json.RawMessage(`{}`), []byte("nonce"), []byte("cipher")); err == nil {
		t.Fatal("accepted missing claim")
	}
	if _, _, err = s.DatabaseWithUsers(ctx, "db-a"); err != sql.ErrNoRows {
		t.Fatalf("metadata persisted after failed claim: %v", err)
	}
	before, err := s.GetJob(ctx, j.ID)
	if err != nil || before.Step != "" || len(before.Result) != 0 {
		t.Fatalf("partial publication: %+v %v", before, err)
	}
	if err = s.PutClaim(ctx, JobClaim{JobID: j.ID, ClaimHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	result := json.RawMessage(`{"db_id":"db-a"}`)
	if err = s.CommitDatabaseClaim(ctx, j.ID, j.Verb, j.ObjectKey, result, []byte("nonce"), []byte("cipher")); err != nil {
		t.Fatal(err)
	}
	if _, users, err := s.DatabaseWithUsers(ctx, "db-a"); err != nil || len(users) != 1 || !users[0].Owner {
		t.Fatalf("metadata not published with claim: %v %+v", err, users)
	}
	stored, err := s.GetJob(ctx, j.ID)
	if err != nil || stored.Step != "database_committed" || string(stored.Result) != string(result) {
		t.Fatalf("job: %+v %v", stored, err)
	}
	if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "owner"); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitDatabaseClaim(ctx, j.ID, j.Verb, j.ObjectKey, result, []byte("nonce"), []byte("cipher")); err == nil {
		t.Fatal("replay republished consumed claim")
	}
	if _, err = s.ConsumeClaim(ctx, j.ID, "hash", "owner"); !errorsIsNoRows(err) {
		t.Fatalf("claim recreated: %v", err)
	}
}
func errorsIsNoRows(err error) bool { return err == sql.ErrNoRows }

package jobs

import (
	"encoding/json"
	"testing"
)

func TestDatabaseClaimPublication(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, err := q.Enqueue(ctx, "database.create", map[string]string{"id": "db-a"}, "db:db-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, r.Job.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = q.PublishDatabaseClaim(ctx, &r.Job, json.RawMessage(`{"db_id":"db-a"}`), map[string]any{"password": "test-secret"}); err != nil {
		t.Fatal(err)
	}
	if r.Job.Step != "database_committed" || string(r.Job.Result) != `{"db_id":"db-a"}` {
		t.Fatal("worker job missing committed checkpoint")
	}
	got, err := q.Claim(ctx, r.Job.ID, r.ClaimToken)
	if err != nil || got["password"] != "test-secret" {
		t.Fatalf("claim: %v %v", got, err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil || j.Step != "database_committed" {
		t.Fatalf("job: %+v %v", j, err)
	}
	if err = q.PublishDatabaseClaim(ctx, &r.Job, json.RawMessage(`{}`), map[string]any{"password": "another"}); err == nil {
		t.Fatal("republished claim")
	}
}

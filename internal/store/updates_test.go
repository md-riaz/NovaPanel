package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

func TestJobUpdatesListsEventsAndRecovery(t *testing.T) {
	s := fresh(t)
	ctx := context.Background()
	for _, j := range []Job{{ID: "a", Verb: "ensure", Actor: "trusted", Role: "owner", CreatedBy: "owner", Args: json.RawMessage(`{}`), State: "running", Created: 1}, {ID: "b", Verb: "ensure", Args: json.RawMessage(`{}`), State: "queued", Created: 2}} {
		if e := s.CreateJob(ctx, j); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.RecoverJobs(ctx, 10); e != nil {
		t.Fatal(e)
	}
	j, e := s.GetJob(ctx, "a")
	if e != nil || j.State != "queued" {
		t.Fatal(j, e)
	}
	j.State = "done"
	j.Actor = "forged"
	j.Role = "read_only"
	j.Verb = "forged"
	j.CreatedBy = "forged"
	j.Result = json.RawMessage(`{"safe":true}`)
	if e = s.UpdateJob(ctx, j); e != nil {
		t.Fatal(e)
	}
	j, e = s.GetJob(ctx, "a")
	if e != nil || j.Actor != "trusted" || j.Role != "owner" || j.Verb != "ensure" || j.CreatedBy != "owner" || string(j.Result) != `{"safe":true}` {
		t.Fatal(j, e)
	}
	list, e := s.ListJobs(ctx, JobFilter{Verb: "ensure", Limit: 1})
	if e != nil || len(list) != 1 || list[0].ID != "b" {
		t.Fatal(list, e)
	}
	list, e = s.ListJobs(ctx, JobFilter{Before: "b", State: "done"})
	if e != nil || len(list) != 1 || list[0].ID != "a" {
		t.Fatal(list, e)
	}
	v := JobEvent{ID: "ev", JobID: "a", TS: 10, Msg: "safe"}
	if e = s.AppendJobEvent(ctx, v); e != nil {
		t.Fatal(e)
	}
	ev, e := s.JobEvents(ctx, "a")
	if e != nil || len(ev) != 1 || ev[0] != v {
		t.Fatal(ev, e)
	}
	if e = s.PutClaim(ctx, JobClaim{JobID: "b", ClaimHash: "h", Expires: 10}); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteExpiredClaims(ctx, 10); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishClaim(ctx, "b", []byte("n"), []byte("c")); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
}
func TestCreateJobWithClaimIsAtomic(t *testing.T) {
	s := fresh(t)
	ctx := context.Background()
	j := Job{ID: "a", Verb: "ensure", Args: json.RawMessage(`{}`), State: "queued"}
	bad := JobClaim{JobID: "missing", ClaimHash: "hash", Expires: 100}
	if e := s.CreateJobWithClaim(ctx, j, bad); e == nil {
		t.Fatal("accepted mismatched claim")
	}
	if _, e := s.GetJob(ctx, "a"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("job escaped failed enqueue", e)
	}
	good := JobClaim{JobID: "a", ClaimHash: "hash", Expires: 100}
	if e := s.CreateJobWithClaim(ctx, j, good); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetJob(ctx, "a"); e != nil {
		t.Fatal(e)
	}
}

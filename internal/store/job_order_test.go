package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestQueuedJobsUseAdmissionOrderWithinSameMillisecond(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for _, id := range []string{"z-earlier", "a-later"} {
		j := Job{ID: id, Verb: "test", ObjectKey: "db:same", State: "queued", Created: 42, Updated: 42, Args: json.RawMessage(`{}`)}
		if err := s.CreateJobWithClaim(ctx, j, JobClaim{JobID: id, ClaimHash: id}); err != nil {
			t.Fatal(err)
		}
	}
	j, err := s.NextQueuedJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if j.ID != "z-earlier" {
		t.Fatalf("got %s, want first admitted job", j.ID)
	}
}

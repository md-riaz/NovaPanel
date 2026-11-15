package store

import (
	"context"
	"fmt"
	"testing"
)

func TestListQueuedJobsPagesInRowIDOrder(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	for i := 0; i < 405; i++ {
		state := "queued"
		if i == 201 {
			state = "done"
		}
		j := Job{ID: fmt.Sprintf("%04d", 9999-i), Verb: "test", ObjectKey: "key", State: state, Args: []byte(`{}`), Created: 42, Updated: 42}
		if err := s.CreateJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	highWater, err := s.QueuedJobsHighWater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateJob(ctx, Job{ID: "new", Verb: "test", ObjectKey: "key", State: "queued", Args: []byte(`{}`), Created: 42, Updated: 42}); err != nil {
		t.Fatal(err)
	}
	var cursor int64
	count := 0
	for {
		page, err := s.ListQueuedJobs(ctx, cursor, highWater)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > QueuedJobsPageSize {
			t.Fatal("page exceeded bound")
		}
		if len(page) == 0 {
			break
		}
		for _, entry := range page {
			if entry.RowID <= cursor {
				t.Fatalf("rowid %d after %d", entry.RowID, cursor)
			}
			cursor = entry.RowID
			i := count
			if i >= 201 {
				i++
			}
			if want := fmt.Sprintf("%04d", 9999-i); entry.Job.ID != want || entry.Job.State != "queued" {
				t.Fatalf("got %+v, want %s queued", entry.Job, want)
			}
			count++
		}
	}
	if count != 404 {
		t.Fatalf("got %d jobs", count)
	}
}

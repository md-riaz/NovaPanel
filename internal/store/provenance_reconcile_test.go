package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestProvenanceIdentityMatchesDurableCreate(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	j := Job{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", Verb: "database.create", ObjectKey: "db:" + db, State: "failed", Args: json.RawMessage(`{"db_id":"` + db + `","user_id":"` + user + `","name":"sample_db","username":"sample_user"}`)}
	if err := s.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProvenanceIdentity(ctx, "user", "sample_user", user)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Known || got.Live || got.Resource || got.DatabaseID != db {
		t.Fatalf("identity: %+v", got)
	}
	bad, err := s.ProvenanceIdentity(ctx, "user", "other_user", user)
	if err != nil {
		t.Fatal(err)
	}
	if bad.Known {
		t.Fatalf("name mismatch accepted: %+v", bad)
	}
	j.Code = "interrupted"
	if err := s.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err = s.ProvenanceIdentity(ctx, "user", "sample_user", user)
	if err != nil || !got.Live {
		t.Fatalf("interrupted job not protected: %+v %v", got, err)
	}
}

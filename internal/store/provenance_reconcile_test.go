package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestProvenanceIdentityMatchesDeleteAfterResourceRemoved(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	for _, j := range []Job{
		{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", Verb: "database.delete", ObjectKey: "db:" + db, State: "done", Args: json.RawMessage(`{"db_id":"` + db + `","name":"sample_db"}`)},
		{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAY", Verb: "dbuser.delete", ObjectKey: "db:" + db, State: "done", Args: json.RawMessage(`{"db_id":"` + db + `","dbuser_id":"` + user + `","username":"sample_user","db_name":"sample_db"}`)},
	} {
		if err := s.CreateJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ kind, name, id string }{{"schema", "sample_db", db}, {"user", "sample_user", user}} {
		got, err := s.ProvenanceIdentity(ctx, tc.kind, tc.name, tc.id)
		if err != nil || !got.Known || got.Resource || got.Live || got.DatabaseID != db {
			t.Errorf("%s identity: %+v %v", tc.kind, got, err)
		}
	}
}

func TestProvenanceIdentityMatchesCascadeDeleteAfterCreateJobsGone(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	j := Job{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", Verb: "database.delete", ObjectKey: "db:" + db, State: "done", Args: json.RawMessage(`{"db_id":"` + db + `","name":"sample_db","users":[{"id":"` + user + `","username":"sample_user"}]}`)}
	if err := s.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProvenanceIdentity(ctx, "user", "sample_user", user)
	if err != nil || !got.Known || got.DatabaseID != db {
		t.Fatalf("cascade identity lost: %+v %v", got, err)
	}
}

func TestDeleteDatabaseForJobPersistsCascadeIdentity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	const job = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	if _, err = s.db.ExecContext(ctx, `INSERT INTO databases(id,name,created) VALUES(?,?,1)`, db, "sample_db"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO db_users(id,db_id,username,created) VALUES(?,?,?,1)`, user, db, "sample_user"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateJob(ctx, Job{ID: job, Verb: "database.delete", ObjectKey: "db:" + db, State: "running", Args: json.RawMessage(`{"db_id":"` + db + `","name":"sample_db"}`)}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDatabaseForJob(ctx, db, "sample_db", job); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProvenanceIdentity(ctx, "user", "sample_user", user)
	if err != nil || !got.Known || got.Resource || !got.Live || got.DatabaseID != db {
		t.Fatalf("cascade evidence: %+v %v", got, err)
	}
}

// A failure at the cascade statement must roll back the preceding identity snapshot.
func TestDeleteDatabaseForJobCascadeRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const db = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	const user = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	const job = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	if _, err = s.db.ExecContext(ctx, `INSERT INTO databases(id,name,created) VALUES(?,?,1)`, db, "sample_db"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO db_users(id,db_id,username,created) VALUES(?,?,?,1)`, user, db, "sample_user"); err != nil {
		t.Fatal(err)
	}
	original := `{"db_id":"` + db + `","name":"sample_db"}`
	if err = s.CreateJob(ctx, Job{ID: job, Verb: "database.delete", ObjectKey: "db:" + db, State: "running", Args: json.RawMessage(original)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_cascade BEFORE DELETE ON databases BEGIN SELECT RAISE(ABORT, 'injected cascade failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDatabaseForJob(ctx, db, "sample_db", job); err == nil {
		t.Fatal("cascade injection not reached")
	}
	j, err := s.GetJob(ctx, job)
	if err != nil || string(j.Args) != original {
		t.Fatalf("snapshot not rolled back: %s %v", j.Args, err)
	}
	_, users, err := s.DatabaseWithUsers(ctx, db)
	if err != nil || len(users) != 1 || users[0].ID != user {
		t.Fatalf("cascade not rolled back: %+v %v", users, err)
	}
	if _, err = s.db.ExecContext(ctx, `DROP TRIGGER fail_cascade`); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDatabaseForJob(ctx, db, "sample_db", job); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProvenanceIdentity(ctx, "user", "sample_user", user)
	if err != nil || !got.Known || got.Resource || !got.Live || got.DatabaseID != db {
		t.Fatalf("replay lost identity: %+v %v", got, err)
	}
}

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

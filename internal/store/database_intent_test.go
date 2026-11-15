package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDatabaseIntentAtomicReservationAndSecret(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	j := Job{ID: "job1", Verb: "database.create", ObjectKey: "db:sample", Args: json.RawMessage(`{"name":"sample"}`), State: "queued"}
	d := Database{ID: "db1", Name: "sample", Engine: "mariadb"}
	u := DatabaseUser{ID: "user1", DatabaseID: d.ID, Username: "account"}
	c := JobClaim{JobID: j.ID, ClaimHash: "hash1"}
	if err = s.CreateDatabaseIntent(ctx, j, c, []byte("nonce"), []byte("sealed"), &d, &u); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckDatabaseReservation(ctx, d, u); err == nil {
		t.Fatal("resource metadata published before successful creation")
	}
	nonce, ciphertext, err := s.DatabaseIntentSecret(ctx, j.ID)
	if err != nil || string(nonce) != "nonce" || string(ciphertext) != "sealed" {
		t.Fatalf("secret: %q %q %v", nonce, ciphertext, err)
	}
	j.ID = "job2"
	c.JobID = j.ID
	c.ClaimHash = "hash2"
	d.ID = "db2"
	u.ID = "user2"
	u.DatabaseID = d.ID
	if err = s.CreateDatabaseIntent(ctx, j, c, []byte("nonce"), []byte("sealed"), &d, &u); err != nil {
		t.Fatalf("admission must not reserve names: %v", err)
	}
}

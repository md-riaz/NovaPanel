package store

import (
	"context"
	"testing"
)

func TestDatabasePrimaryUserLookup(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	d := Database{ID: "db-a", Name: "d_example", Engine: "mariadb"}
	u := DatabaseUser{ID: "user-a", DatabaseID: d.ID, Username: "u_example"}
	if err = s.ReserveDatabase(ctx, d, u); err != nil {
		t.Fatal(err)
	}
	second := DatabaseUser{ID: "user-b", DatabaseID: d.ID, Username: "u_other"}
	if err = s.ReserveDatabaseUser(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveDatabaseUser(ctx, second); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	got, users, err := s.DatabaseWithUsers(ctx, d.ID)
	if err != nil || got.Name != d.Name || len(users) != 2 {
		t.Fatalf("lookup: %+v %+v %v", got, users, err)
	}
	if _, _, err = s.DatabaseWithUsers(ctx, "missing"); err == nil {
		t.Fatal("missing database accepted")
	}
}

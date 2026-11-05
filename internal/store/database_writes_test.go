package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestDatabaseOwnerIsUnique(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	d := Database{ID: "db", Name: "d_test", Engine: "mariadb"}
	if err := s.ReserveDatabase(ctx, d, DatabaseUser{ID: "owner", DatabaseID: d.ID, Username: "u_owner"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveDatabaseUser(ctx, DatabaseUser{ID: "secondary", DatabaseID: d.ID, Username: "u_secondary"}); err != nil {
		t.Fatal(err)
	}
	_, users, err := s.DatabaseWithUsers(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("users: %+v", users)
	}
	owners := 0
	for _, u := range users {
		if u.Owner {
			owners++
			if u.ID != "owner" {
				t.Fatal("wrong owner")
			}
		}
	}
	if owners != 1 {
		t.Fatalf("owners: %d", owners)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE db_users SET owner=1 WHERE id='secondary'`); err == nil {
		t.Fatal("accepted second owner")
	}
}

func TestDatabaseReservationIsIdempotentAndRejectsCollision(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	db := Database{ID: "db-a", Name: "d_example", Engine: "mariadb"}
	user := DatabaseUser{ID: "user-a", DatabaseID: "db-a", Username: "u_example"}
	if err = s.CheckDatabaseReservation(ctx, db, user); err == nil {
		t.Fatal("accepted missing reservation")
	}
	if err = s.ReserveDatabase(ctx, db, user); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckDatabaseReservation(ctx, db, user); err != nil {
		t.Fatal(err)
	}
	wrong := user
	wrong.Username = "u_other"
	if err = s.CheckDatabaseReservation(ctx, db, wrong); err == nil {
		t.Fatal("accepted mismatched username")
	}
	wrong = user
	wrong.DatabaseID = "db-other"
	if err = s.CheckDatabaseReservation(ctx, db, wrong); err == nil {
		t.Fatal("accepted mismatched database")
	}
	otherSite := "different-site"
	altered := db
	altered.SiteID = &otherSite
	if err = s.CheckDatabaseReservation(ctx, altered, user); err == nil {
		t.Fatal("accepted mismatched site")
	}
	if err = s.ReserveDatabase(ctx, db, user); err != nil {
		t.Fatalf("replay: %v", err)
	}
	other := db
	other.ID = "db-b"
	otherUser := user
	otherUser.ID = "user-b"
	otherUser.DatabaseID = other.ID
	if err = s.ReserveDatabase(ctx, other, otherUser); err == nil {
		t.Fatal("accepted duplicate name")
	}
	// A duplicate account must roll back the newly inserted database.
	other.Name = "d_other"
	if err = s.ReserveDatabase(ctx, other, otherUser); err == nil {
		t.Fatal("accepted duplicate account")
	}
	rows, err := s.ListDatabases(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows: %+v %v", rows, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM db_users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("users: %d %v", count, err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM databases WHERE id=?", db.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM db_users").Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade: %d %v", count, err)
	}
}

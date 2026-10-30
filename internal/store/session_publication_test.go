package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestVerifiedRotationRehashRollback(t *testing.T) {
	ctx := context.Background()
	c := &authClock{1800000000000}
	s, e := OpenWithClock(":memory:", c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	u := User{ID: "u", Username: "reader", Email: "e", PWPHC: "verified", Role: "read_only"}
	if e = s.CreateUser(ctx, u); e != nil {
		t.Fatal(e)
	}
	anon := Session{ID: "anon", TokenHash: "a", Kind: "anon", CSRFToken: "a", IdleDeadline: c.now + 1000, AbsDeadline: c.now + 2000}
	if e = s.CreateSession(ctx, anon); e != nil {
		t.Fatal(e)
	}
	row := anon
	row.ID = "replacement"
	row.Kind = "user"
	row.UserID = &u.ID
	row.Role = &u.Role
	if e = s.CreateSession(ctx, row); e != nil {
		t.Fatal(e)
	}
	if e = s.RotateVerifiedSession(ctx, anon.ID, row, u, "rehashed"); !errors.Is(e, ErrAuthConflict) {
		t.Fatalf("duplicate: %v", e)
	}
	current, e := s.UserByID(ctx, u.ID)
	if e != nil || current.PWPHC != u.PWPHC {
		t.Fatalf("failed publication rehashed: %+v %v", current, e)
	}
	if _, e = s.GetSession(ctx, anon.ID); e != nil {
		t.Fatalf("rollback deleted anon: %v", e)
	}
	row.ID = "fresh"
	if e = s.RotateVerifiedSession(ctx, anon.ID, row, u, "rehashed"); e != nil {
		t.Fatal(e)
	}
	current, e = s.UserByID(ctx, u.ID)
	if e != nil || current.PWPHC != "rehashed" {
		t.Fatalf("rehash: %+v %v", current, e)
	}
	if _, e = s.GetSession(ctx, row.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetSession(ctx, anon.ID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("old session: %v", e)
	}
	if _, e = s.GetSession(ctx, "replacement"); e != nil {
		t.Fatalf("rehash revoked another session: %v", e)
	}
}

func TestVerifiedRotationRejectsChangedActiveMFA(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active any
	}{{"disabled", nil}, {"replaced", "replacement"}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			c := &authClock{1800000000000}
			s, e := OpenWithClock(":memory:", c)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			u := User{ID: "u", Username: "reader", Email: "e", PWPHC: "verified", Role: "read_only"}
			if e = s.CreateUser(ctx, u); e != nil {
				t.Fatal(e)
			}
			// Seed the already-verified snapshot, then simulate disable or replacement.
			old := "old-active"
			u.TOTPSecret = &old
			if _, e = s.db.ExecContext(ctx, "UPDATE users SET totp_secret=? WHERE id=?", tc.active, u.ID); e != nil {
				t.Fatal(e)
			}
			anon := Session{ID: "anon", TokenHash: "a", Kind: "anon", CSRFToken: "a", IdleDeadline: c.now + 1000, AbsDeadline: c.now + 2000}
			if e = s.CreateSession(ctx, anon); e != nil {
				t.Fatal(e)
			}
			row := anon
			row.ID = "fresh"
			row.Kind = "user"
			row.UserID = &u.ID
			row.Role = &u.Role
			row.MFAAt = &c.now
			if e = s.RotateVerifiedSession(ctx, anon.ID, row, u, "rehashed"); !errors.Is(e, ErrAuthConflict) {
				t.Fatalf("stale MFA published: %v", e)
			}
			current, e := s.UserByID(ctx, u.ID)
			if e != nil || current.PWPHC != u.PWPHC {
				t.Fatalf("rehashed on conflict: %+v %v", current, e)
			}
			if _, e = s.GetSession(ctx, anon.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = s.GetSession(ctx, row.ID); !errors.Is(e, sql.ErrNoRows) {
				t.Fatalf("new session: %v", e)
			}
		})
	}
}

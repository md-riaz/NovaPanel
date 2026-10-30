package store

import (
	"context"
	"testing"
)

func TestAuthIdentityLifecycle(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	u := User{ID: "user", Username: "owner", Email: "o@example.test", PWPHC: "hash", Role: "owner"}
	if e = s.CreateUser(ctx, u); e != nil {
		t.Fatal(e)
	}
	got, e := s.UserByName(ctx, "owner")
	if e != nil || got.TOTPSecret != nil || got.Created == 0 {
		t.Fatal(got, e)
	}
	role := "admin"
	uid := "user"
	ss := Session{ID: "session", TokenHash: "digest", Kind: "user", UserID: &uid, Role: &u.Role, CSRFToken: "csrf", IdleDeadline: 1 << 62, AbsDeadline: 1 << 62}
	if e = s.CreateSession(ctx, ss); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateUser(ctx, uid, UserPatch{Role: &role}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetSession(ctx, ss.ID); e == nil {
		t.Fatal("role transition left session")
	}
}

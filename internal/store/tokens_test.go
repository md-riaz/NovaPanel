package store

import (
	"context"
	"errors"
	"testing"
)

func TestAPITokenLifecycle(t *testing.T) {
	ctx := context.Background()
	c := &authClock{1800000000000}
	s, e := OpenWithClock(":memory:", c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.CreateUser(ctx, User{ID: "u", Username: "owner", Email: "e", PWPHC: "phc", Role: "owner"}); e != nil {
		t.Fatal(e)
	}
	expiry := c.now + 1000
	token := APIToken{ID: "token", Name: "deploy", SecretPHC: "phc", Scopes: []string{"deploy"}, CreatedBy: "u", Expires: &expiry}
	if e = s.CreateAPIToken(ctx, token); e != nil {
		t.Fatal(e)
	}
	if e = s.TouchAPIToken(ctx, token.ID, "wrong"); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	if e = s.TouchAPIToken(ctx, token.ID, "phc"); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetAPIToken(ctx, token.ID)
	if e != nil || got.LastUsed == nil || *got.LastUsed != c.now || len(got.Scopes) != 1 {
		t.Fatal(got, e)
	}
	c.now = expiry
	if _, e = s.GetAPIToken(ctx, token.ID); !errors.Is(e, ErrAuthExpired) {
		t.Fatal(e)
	}
	c.now--
	if e = s.RevokeAPIToken(ctx, token.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetAPIToken(ctx, token.ID); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	list, e := s.ListAPITokens(ctx)
	if e != nil || len(list) != 1 || !list[0].Revoked {
		t.Fatal(list, e)
	}
	if e = s.DeleteUser(ctx, "u"); e != nil {
		t.Fatal(e)
	}
	list, e = s.ListAPITokens(ctx)
	if e != nil || len(list) != 0 {
		t.Fatal(list, e)
	}
}

package verbs_test

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/store"
	"reflect"
	"strings"
	"testing"
)

func TestSessionStartExpiryCleanupOnlyOnFreshMint(t *testing.T) {
	s, c, call := sessionHarness(t)
	ctx := context.Background()
	first := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	oldID := strings.Split(first, "_")[1]
	c.now += 600001
	fresh := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	if _, e := s.Agent.Store.GetSession(ctx, oldID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("expired row retained after fresh mint: %v", e)
	}
	// Seed an expiring row without a subsequent fresh mint.
	row := store.Session{ID: "expiry-fixture", TokenHash: "fixture-hash", Kind: "anon", CSRFToken: "fixture-csrf", IdleDeadline: c.now + 1, AbsDeadline: c.now + 1}
	if e := s.Agent.Store.CreateSession(ctx, row); e != nil {
		t.Fatal(e)
	}
	c.now += 2
	sessionData(t, call("session.start", fresh, `{}`))
	if _, e := s.Agent.Store.GetSession(ctx, row.ID); !errors.Is(e, store.ErrAuthExpired) {
		t.Fatalf("reuse swept row: %v", e)
	}
	sessionData(t, call("session.start", "", `{}`))
	if _, e := s.Agent.Store.GetSession(ctx, row.ID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatalf("fresh mint failed cleanup: %v", e)
	}
}

func TestSessionStartReusesAnonymousWithoutTouchOrMintCharge(t *testing.T) {
	s, c, call := sessionHarness(t)
	s.Agent.Cfg.AnonMintRPS = 1
	s.Agent.Cfg.AnonMintBurst = 10
	first := sessionData(t, call("session.start", "", `{}`))
	token := first["session"].(string)
	sid := strings.Split(token, "_")[1]
	before, e := s.Agent.Store.GetSession(context.Background(), sid)
	if e != nil {
		t.Fatal(e)
	}
	c.now += 50000
	for i := 0; i < 20; i++ {
		if got := sessionData(t, call("session.start", token, `{}`)); !reflect.DeepEqual(first, got) {
			t.Fatalf("changed %+v", got)
		}
	}
	after, e := s.Agent.Store.GetSession(context.Background(), sid)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("anonymous reuse touched row")
	}
	for i := 0; i < 10; i++ {
		sessionData(t, call("session.start", "", `{}`))
	}
	if r := call("session.start", "", `{}`); r.Code != "rate_limited" {
		t.Fatalf("fresh mint budget %+v", r)
	}
}
func TestSessionStartInvalidAndExpiredCredentialsMintFresh(t *testing.T) {
	for _, kind := range []string{"malformed", "forged", "expired"} {
		t.Run(kind, func(t *testing.T) {
			_, c, call := sessionHarness(t)
			first := sessionData(t, call("session.start", "", `{}`))
			token := first["session"].(string)
			switch kind {
			case "malformed":
				token = "invalid"
			case "forged":
				token = token[:len(token)-1] + "g"
			case "expired":
				c.now += 600000
			}
			next := sessionData(t, call("session.start", token, `{}`))
			if next["session"] == first["session"] || next["csrf_token"] == first["csrf_token"] {
				t.Fatal("not fresh")
			}
		})
	}
}
func TestSessionStartRejectsAuthenticatedCaller(t *testing.T) {
	_, _, call := sessionHarness(t)
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	if r := call("session.start", token, `{}`); r.Code != "precondition_failed" {
		t.Fatalf("authenticated start %+v", r)
	}
	sessionData(t, call("session.whoami", token, `{}`))
}

func TestSessionStartRejectsDisabledUserAsAuthenticated(t *testing.T) {
	s, _, call := sessionHarness(t)
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	u, e := s.Agent.Store.UserByName(context.Background(), "reader")
	if e != nil {
		t.Fatal(e)
	}
	disabled := true
	if e = s.Agent.Store.UpdateUser(context.Background(), u.ID, store.UserPatch{Disabled: &disabled}); e != nil {
		t.Fatal(e)
	}
	fresh := sessionData(t, call("session.start", token, `{}`))
	if fresh["session"] == token {
		t.Fatal("disabled session reused")
	}
	if r := call("session.whoami", token, `{}`); r.Code != "unauthenticated" {
		t.Fatalf("disabled user %+v", r)
	}
}

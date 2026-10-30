package verbs_test

import (
	"context"
	"novapanel/internal/auth"
	"novapanel/internal/store"
	"testing"
)

// The third agent-clock read is token minting, after credential verification.
// Store clocks stay separate so the mutation commits before publication begins.
type loginPublicationClock struct {
	now   int64
	calls int
	hook  func()
}

func (c *loginPublicationClock) Now() int64 {
	c.calls++
	if c.calls == 3 {
		c.hook()
	}
	return c.now
}

func TestLoginRejectsChangedAuthenticationState(t *testing.T) {
	for _, mutation := range []string{"password", "rehash_password", "mfa", "role", "disabled"} {
		t.Run(mutation, func(t *testing.T) {
			s, c, call := sessionHarness(t)
			ctx := context.Background()
			anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
			u, e := s.Agent.Store.UserByName(ctx, "reader")
			if e != nil {
				t.Fatal(e)
			}
			replacement := auth.Hash("replacement password")
			password := "correct password"
			if mutation == "rehash_password" {
				password = "known"
				if e = s.Agent.Store.SetPassword(ctx, u.ID, "$argon2id$v=19$m=8192,t=1,p=1$KioqKioqKioqKioqKioqKg$yj601DUth0a4EptCqIumdHhvbbS9VSmIznW3AoXb2n4"); e != nil {
					t.Fatal(e)
				}
			}
			if mutation == "mfa" {
				if e = s.Agent.Store.SetPendingMFA(ctx, u.ID, "sealed-pending"); e != nil {
					t.Fatal(e)
				}
			}
			clock := &loginPublicationClock{now: c.now, hook: func() {
				var err error
				switch mutation {
				case "password", "rehash_password":
					err = s.Agent.Store.SetPassword(ctx, u.ID, replacement)
				case "mfa":
					codes := make([]store.BackupCode, 8)
					for i := range codes {
						codes[i] = store.BackupCode{ID: sessionID(t), UserID: u.ID, CodePHC: "phc"}
					}
					err = s.Agent.Store.ConfirmMFA(ctx, u.ID, "sealed-pending", c.now/30000, 300000, codes)
				case "role":
					role := "admin"
					err = s.Agent.Store.UpdateUser(ctx, u.ID, store.UserPatch{Role: &role})
				case "disabled":
					disabled := true
					err = s.Agent.Store.UpdateUser(ctx, u.ID, store.UserPatch{Disabled: &disabled})
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			s.Agent.Clock = clock
			r := call("session.login", anon, `{"user":"reader","password":"`+password+`"}`)
			if clock.calls < 3 {
				t.Fatal("mutation did not run")
			}
			if r.OK || r.Code != "unauthenticated" {
				t.Fatalf("changed %s published login: %+v", mutation, r)
			}
			if _, e = s.Sessions.Resolve(ctx, anon); e != nil {
				t.Fatalf("failed publication consumed anonymous session: %v", e)
			}
		})
	}
}

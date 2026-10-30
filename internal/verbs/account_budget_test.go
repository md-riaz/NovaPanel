package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/auth"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"testing"
)

func TestPasswordFailureBudgetsAreAccountBasedAndReset(t *testing.T) {
	s, _, call := sessionHarness(t)
	s.Agent.Cfg.LoginMaxAttempts = 2
	if e := s.Agent.Store.CreateUser(context.Background(), store.User{ID: sessionID(t), Username: "other", Email: "other@example.test", PWPHC: auth.Hash("correct password"), Role: "read_only"}); e != nil {
		t.Fatal(e)
	}
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	login := func(user, pw string) rpc.Reply {
		b, _ := json.Marshal(map[string]string{"user": user, "password": pw})
		return call("session.login", anon, string(b))
	}
	for i := 0; i < 2; i++ {
		if r := login("reader", "wrong"); r.Code != "unauthenticated" {
			t.Fatal(r)
		}
	}
	if r := login("reader", "correct password"); r.Code != "unauthenticated" {
		t.Fatalf("uniform exhaustion %+v", r)
	}
	if r := login("other", "correct password"); !r.OK {
		t.Fatalf("account isolation %+v", r)
	}
	anon = sessionData(t, call("session.start", "", `{}`))["session"].(string)
	for i := 0; i < 4; i++ {
		if r := login("other", "wrong"); r.Code != "unauthenticated" {
			t.Fatal(r)
		}
		sessionData(t, login("other", "correct password"))
		anon = sessionData(t, call("session.start", "", `{}`))["session"].(string)
	}
}

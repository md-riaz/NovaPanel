package verbs_test

import (
	"context"
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"net/url"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"testing"
	"time"
)

func TestSecondFactorFailureBudgetSharedAcrossVerbsAndBackup(t *testing.T) {
	s, c, call := sessionHarness(t)
	s.Agent.Cfg.TOTPMaxAttempts = 2
	seal, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	s.Agent.Secret = seal
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	uri := sessionData(t, call("mfa.enroll", token, `{}`))["otpauth_uri"].(string)
	u, e := url.Parse(uri)
	if e != nil {
		t.Fatal(e)
	}
	proof := func() string {
		code, e := totp.GenerateCode(u.Query().Get("secret"), time.UnixMilli(c.now))
		if e != nil {
			t.Fatal(e)
		}
		b, _ := json.Marshal(map[string]string{"totp": code})
		return string(b)
	}
	for _, verb := range []string{"mfa.confirm", "session.reauth"} {
		if r := call(verb, token, `{"totp":"wrong"}`); r.Code != "unauthenticated" {
			t.Fatal(r)
		}
	}
	if r := call("mfa.confirm", token, proof()); r.Code != "unauthenticated" {
		t.Fatalf("cross-verb bypass %+v", r)
	}
	c.now += 900000
	// Renew the expired session without clearing the user's second-factor budget.
	anon = sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token = sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	uri = sessionData(t, call("mfa.enroll", token, `{}`))["otpauth_uri"].(string)
	u, e = url.Parse(uri)
	if e != nil {
		t.Fatal(e)
	}
	codes := sessionData(t, call("mfa.confirm", token, proof()))["backup_codes"].([]any)
	c.now += 30000
	for round := 0; round < 2; round++ {
		if r := call("session.reauth", token, `{"totp":"wrong"}`); r.Code != "unauthenticated" {
			t.Fatal(r)
		}
		b, _ := json.Marshal(map[string]string{"totp": codes[round].(string)})
		token = sessionData(t, call("session.reauth", token, string(b)))["session"].(string)
	}
	// Password success does not clear second-factor failures. Login and disable share reauth's budget.
	anon = sessionData(t, call("session.start", "", `{}`))["session"].(string)
	for _, verb := range []string{"session.reauth", "mfa.disable"} {
		caller, e := s.Sessions.Resolve(context.Background(), token)
		if e != nil {
			t.Fatal(e)
		}
		caller.MFAat = c.now
		r := s.Dispatch(rpc.WithCaller(context.Background(), caller), rpc.Request{Verb: verb, Args: []byte(`{"totp":"wrong"}`)})
		if r.Code != "unauthenticated" {
			t.Fatal(r)
		}
	}
	b, _ := json.Marshal(map[string]string{"user": "reader", "password": "correct password", "totp": codes[2].(string)})
	if r := call("session.login", anon, string(b)); r.Code != "unauthenticated" {
		t.Fatalf("login bypass %+v", r)
	}
	b, _ = json.Marshal(map[string]string{"totp": codes[2].(string)})
	if r := call("session.reauth", token, string(b)); r.Code != "unauthenticated" {
		t.Fatalf("backup bypass %+v", r)
	}
}

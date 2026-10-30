package verbs_test

import (
	"context"
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"net/url"
	"novapanel/internal/auth"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"strings"
	"testing"
	"time"
)

func TestMFAEnrollmentLifecycle(t *testing.T) {
	s, c, call := sessionHarness(t)
	// This lifecycle intentionally exercises six proofs in one window.
	s.Agent.Cfg.TOTPMaxAttempts = 6
	s.Agent.Secret, _ = secret.New(make([]byte, 32))
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	var events []rpc.AuditEvent
	s.Audit = rpc.AuditFunc(func(e rpc.AuditEvent) { events = append(events, e) })
	enroll := sessionData(t, call("mfa.enroll", token, `{}`))
	uri, e := url.Parse(enroll["otpauth_uri"].(string))
	if e != nil {
		t.Fatal(e)
	}
	key := uri.Query().Get("secret")
	u, e := s.Agent.Store.UserByName(context.Background(), "reader")
	if e != nil {
		t.Fatal(e)
	}
	if u.TOTPSecret != nil || u.TOTPPending == nil || strings.Contains(*u.TOTPPending, key) {
		t.Fatal("pending must be sealed, not active")
	}
	if r := call("mfa.confirm", token, `{"totp":"invalid"}`); r.Code != "unauthenticated" {
		t.Fatalf("bad proof %+v", r)
	}
	proof, _ := totp.GenerateCode(key, time.UnixMilli(c.now))
	args, _ := json.Marshal(map[string]string{"totp": proof})
	result := sessionData(t, call("mfa.confirm", token, string(args)))
	codes := result["backup_codes"].([]any)
	if len(codes) < 8 || len(codes) > 10 {
		t.Fatal("backup count")
	}
	u, _ = s.Agent.Store.UserByID(context.Background(), u.ID)
	if u.TOTPSecret == nil || u.TOTPPending != nil {
		t.Fatal("not promoted")
	}
	stored, _ := s.Agent.Store.ListBackupCodes(context.Background(), u.ID)
	if ok, _ := auth.Verify(codes[0].(string), stored[0].CodePHC); !ok { // IDs need not sort in issuance order.
		found := false
		for _, v := range stored {
			if ok, _ := auth.Verify(codes[0].(string), v.CodePHC); ok {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("backup not hashed")
		}
	}
	if r := call("mfa.enroll", token, `{}`); r.Code != "conflict" {
		t.Fatalf("active replaced %+v", r)
	}
	if r := call("mfa.confirm", token, string(args)); r.OK {
		t.Fatal("confirm replay")
	}
	if r := call("mfa.disable", token, string(args)); r.Code != "forbidden" {
		t.Fatalf("disable needs stepup %+v", r)
	}
	backupArgs, _ := json.Marshal(map[string]any{"totp": codes[0]})
	fresh := sessionData(t, call("session.reauth", token, string(backupArgs)))["session"].(string)
	if r := call("mfa.disable", fresh, string(backupArgs)); r.Code != "unauthenticated" {
		t.Fatalf("backup replay %+v", r)
	}
	backupArgs, _ = json.Marshal(map[string]any{"totp": codes[1]})
	sessionData(t, call("mfa.disable", fresh, string(backupArgs)))
	u, _ = s.Agent.Store.UserByID(context.Background(), u.ID)
	stored, _ = s.Agent.Store.ListBackupCodes(context.Background(), u.ID)
	if u.TOTPSecret != nil || u.TOTPPending != nil || len(stored) != 0 {
		t.Fatal("disable not cleared")
	}
	audit, _ := json.Marshal(events)
	for _, v := range append([]any{key, token, proof}, codes...) {
		if strings.Contains(string(audit), v.(string)) {
			t.Fatal("audit secret leak")
		}
	}
}

func TestMFAExpiredPendingAndCallerScope(t *testing.T) {
	s, c, call := sessionHarness(t)
	s.Agent.Secret, _ = secret.New(make([]byte, 32))
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	other := store.User{ID: sessionID(t), Username: "other", Email: "other@example.test", PWPHC: auth.Hash("password"), Role: "read_only"}
	if e := s.Agent.Store.CreateUser(context.Background(), other); e != nil {
		t.Fatal(e)
	}
	for _, verb := range []string{"mfa.enroll", "mfa.confirm", "mfa.disable"} {
		args, _ := json.Marshal(map[string]string{"user_id": other.ID, "totp": "123456"})
		if r := call(verb, token, string(args)); r.OK {
			t.Fatalf("cross-user %s", verb)
		}
	}
	uri, _ := url.Parse(sessionData(t, call("mfa.enroll", token, `{}`))["otpauth_uri"].(string))
	c.now += 600000
	// Keep testing pending expiry independently of the browser idle deadline.
	old, _ := s.Agent.Store.GetSession(context.Background(), strings.Split(token, "_")[1])
	_ = old
	anon = sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token = sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	proof, _ := totp.GenerateCode(uri.Query().Get("secret"), time.UnixMilli(c.now))
	args, _ := json.Marshal(map[string]string{"totp": proof})
	if r := call("mfa.confirm", token, string(args)); r.Code != "unauthenticated" {
		t.Fatalf("expired %+v", r)
	}
	u, _ := s.Agent.Store.UserByID(context.Background(), other.ID)
	if u.TOTPPending != nil || u.TOTPSecret != nil {
		t.Fatal("other affected")
	}
}

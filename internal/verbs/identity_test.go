package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"strings"
	"testing"
)

func TestUserManagementCanonicalLifecycle(t *testing.T) {
	s, c, call := sessionHarness(t)
	root := func(verb, args string) rpc.Reply {
		caller := rpc.RootCaller(0)
		caller.MFAat = c.now
		return s.Dispatch(rpc.WithCaller(context.Background(), caller), rpc.Request{ID: sessionID(t), Verb: verb, Args: json.RawMessage(args)})
	}
	created := sessionData(t, root("user.create", `{"username":"operator","email":"op@example.test","role":"admin","password":"initial password"}`))
	uid := created["user_id"].(string)
	args := func(m map[string]any) string { m["user_id"] = uid; b, _ := json.Marshal(m); return string(b) }
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"operator","password":"initial password"}`))["session"].(string)
	if r := call("user.create", token, `{"username":"bad","email":"bad@example.test","role":"owner","password":"password"}`); r.Code != "forbidden" {
		t.Fatalf("admin write %+v", r)
	}
	list := sessionData(t, call("user.list", token, `{}`))
	b, _ := json.Marshal(list)
	for _, v := range []string{"pw_phc", "totp_", "created", "initial password"} {
		if strings.Contains(string(b), v) {
			t.Fatalf("projection %s", b)
		}
	}
	sessionData(t, root("user.update", args(map[string]any{"role": "read_only", "email": "new@example.test"})))
	if r := call("session.whoami", token, `{}`); r.Code != "unauthenticated" {
		t.Fatalf("role old token %+v", r)
	}
	sessionData(t, root("user.set_password", args(map[string]any{"password": "new password"})))
	anon = sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token = sessionData(t, call("session.login", anon, `{"user":"operator","password":"new password"}`))["session"].(string)
	sessionData(t, root("user.delete", args(map[string]any{})))
	if r := call("session.whoami", token, `{}`); r.Code != "unauthenticated" {
		t.Fatalf("delete old token %+v", r)
	}
	if r := root("user.delete", args(map[string]any{})); r.Code != "not_found" {
		t.Fatalf("missing %+v", r)
	}
	for _, a := range []string{`{"username":"x","email":"bad","role":"admin","password":"p"}`, `{"username":"x","email":"x@example.test","role":"root","password":"p"}`, `{"username":"x","email":"x@example.test","role":"admin","password":""}`} {
		if r := root("user.create", a); r.Code != "invalid_argument" {
			t.Fatalf("validation %+v", r)
		}
	}
}
func TestTokenManagementExplicitMetadataAndScopes(t *testing.T) {
	s, c, call := sessionHarness(t)
	ctx := context.Background()
	u, _ := s.Agent.Store.UserByName(ctx, "reader")
	role := "admin"
	if e := s.Agent.Store.UpdateUser(ctx, u.ID, store.UserPatch{Role: &role}); e != nil {
		t.Fatal(e)
	}
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	args := `{"name":"monitor","scopes":["system:read"],"automation":true}`
	if r := call("tokens.create", token, args); r.Code != "forbidden" {
		t.Fatalf("no stepup %+v", r)
	}
	// A persisted session with fresh MFA isolates token issuance from the separately tested MFA lifecycle.
	row, _ := s.Agent.Store.GetSession(ctx, strings.Split(token, "_")[1])
	_ = row
	stepped := rpc.Caller{Kind: "user", Role: "admin", UserID: u.ID, Session: "test-session", Peer: 1001, MFAat: c.now}
	create := func(args string) rpc.Reply {
		return s.Dispatch(rpc.WithCaller(ctx, stepped), rpc.Request{ID: sessionID(t), Verb: "tokens.create", Args: json.RawMessage(args)})
	}
	pat := sessionData(t, create(args))["token"].(string)
	if !strings.HasPrefix(pat, "npat_") {
		t.Fatal("format")
	}
	resolved, e := s.Sessions.Resolve(ctx, pat)
	if e != nil || !resolved.Automation || resolved.UserID != "" || resolved.Role != "" {
		t.Fatalf("caller %+v %v", resolved, e)
	}
	list := sessionData(t, call("tokens.list", token, `{}`))
	b, _ := json.Marshal(list)
	for _, v := range []string{pat, "secret_phc", "created_by", "user_id"} {
		if strings.Contains(string(b), v) {
			t.Fatalf("projection %s", b)
		}
	}
	tid := strings.Split(pat, "_")[1]
	rowToken, e := s.Agent.Store.GetAPIToken(ctx, tid)
	if e != nil || rowToken.CreatedBy != u.ID || rowToken.SecretPHC == pat {
		t.Fatalf("metadata %+v %v", rowToken, e)
	}
	sessionData(t, call("tokens.revoke", token, `{"token_id":"`+tid+`"}`))
	if _, e = s.Sessions.Resolve(ctx, pat); e == nil {
		t.Fatal("revoked resolves")
	}
	for _, a := range []string{`{"name":"x","scopes":["*"]}`, `{"name":"x","scopes":["unknown:write"]}`, `{"name":"x","scopes":[],"expires":1}`} {
		if r := create(a); r.Code != "invalid_argument" {
			t.Fatalf("invalid token %+v", r)
		}
	}
}

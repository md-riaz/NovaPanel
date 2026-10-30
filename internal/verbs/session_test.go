package verbs_test

import (
	"context"
	"encoding/json"
	"github.com/pquerna/otp/totp"
	"novapanel/internal/agent"
	"novapanel/internal/auth"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"strings"
	"testing"
	"time"
)

func sessionID(t *testing.T) string {
	t.Helper()
	v, e := id.New()
	if e != nil {
		t.Fatal(e)
	}
	return v
}

type sessionClock struct{ now int64 }

func (c *sessionClock) Now() int64 { return c.now }
func sessionHarness(t *testing.T) (*rpc.Server, *sessionClock, func(string, string, string) rpc.Reply) {
	t.Helper()
	c := &sessionClock{now: 1800000000000}
	st, e := store.OpenWithClock(":memory:", c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	a := agent.New()
	a.Clock = c
	a.Store = st
	if e = st.CreateUser(context.Background(), store.User{ID: sessionID(t), Username: "reader", Email: "reader@example.test", PWPHC: auth.Hash("correct password"), Role: "read_only"}); e != nil {
		t.Fatal(e)
	}
	s := rpc.NewServer(a)
	s.WebUID = 1001
	call := func(verb, token, args string) rpc.Reply {
		return s.Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Peer: 1001}), rpc.Request{ID: sessionID(t), Verb: verb, Session: token, Args: json.RawMessage(args)})
	}
	return s, c, call
}
func sessionData(t *testing.T, r rpc.Reply) map[string]any {
	t.Helper()
	if !r.OK {
		t.Fatalf("reply: %+v", r)
	}
	b, e := json.Marshal(r.Data)
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if e = json.Unmarshal(b, &m); e != nil {
		t.Fatal(e)
	}
	return m
}
func TestRealSessionLoginLifecycle(t *testing.T) {
	s, _, call := sessionHarness(t)
	var events []rpc.AuditEvent
	s.Audit = rpc.AuditFunc(func(e rpc.AuditEvent) { events = append(events, e) })
	start := sessionData(t, call("session.start", "", `{}`))
	anon := start["session"].(string)
	if r := call("session.whoami", anon, `{}`); r.Code != "unauthenticated" {
		t.Fatalf("anonymous whoami %+v", r)
	}
	for _, args := range []string{`{"user":"missing","password":"wrong"}`, `{"user":"reader","password":"wrong"}`} {
		if r := call("session.login", anon, args); r.Code != "unauthenticated" {
			t.Fatalf("bad login %+v", r)
		}
	}
	login := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))
	token := login["session"].(string)
	if token == anon || login["csrf_token"] == start["csrf_token"] || login["role"] != "read_only" || login["mfa"] != false {
		t.Fatalf("rotation %+v", login)
	}
	if r := call("session.whoami", anon, `{}`); r.Code != "unauthenticated" {
		t.Fatalf("old session %+v", r)
	}
	who := sessionData(t, call("session.whoami", token, `{}`))
	if who["user"] != "reader" || who["role"] != "read_only" || who["csrf_token"] != login["csrf_token"] {
		t.Fatalf("whoami %+v", who)
	}
	if len(who) != 4 {
		t.Fatalf("unexpected projection %+v", who)
	}
	sessionData(t, call("session.logout", token, `{}`))
	if r := call("session.whoami", token, `{}`); r.Code != "unauthenticated" {
		t.Fatalf("logout %+v", r)
	}
	b, _ := json.Marshal(events)
	for _, secret := range []string{anon, token, "correct password", "pw_phc", "totp_secret"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("audit leak %s", b)
		}
	}
	successes, failures := 0, 0
	for _, e := range events {
		if e.Verb == "session.login" {
			if e.Outcome == "ok" {
				successes++
			} else {
				failures++
			}
		}
	}
	if successes != 1 || failures != 2 {
		t.Fatalf("login audit %+v", events)
	}
}

func TestSessionStartPeerRateLimit(t *testing.T) {
	s, c, call := sessionHarness(t)
	for i := 0; i < 10; i++ {
		sessionData(t, call("session.start", "", `{}`))
	}
	if r := call("session.start", "", `{}`); r.Code != "forbidden" {
		t.Fatalf("unbounded mint %+v", r)
	}
	r := s.Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Peer: 1002}), rpc.Request{Verb: "session.start", Args: json.RawMessage(`{}`)})
	if !r.OK {
		t.Fatalf("other peer %+v", r)
	}
	c.now += 60000
	sessionData(t, call("session.start", "", `{}`))
}
func TestRealSessionTimeoutAndForgery(t *testing.T) {
	for _, boundary := range []string{"idle", "absolute"} {
		t.Run(boundary, func(t *testing.T) {
			_, c, call := sessionHarness(t)
			anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
			token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
			forged := token[:len(token)-1] + "g"
			if r := call("session.whoami", forged, `{}`); r.Code != "unauthenticated" {
				t.Fatalf("forged %+v", r)
			}
			if boundary == "absolute" {
				for i := 0; i < 86; i++ {
					c.now += 500000
					sessionData(t, call("session.whoami", token, `{}`))
				}
				c.now += 200000
			} else {
				c.now += 600000
			}
			if r := call("session.whoami", token, `{}`); r.Code != "unauthenticated" {
				t.Fatalf("expired %+v", r)
			}
		})
	}
}
func TestRealMFAReauthRotationAndReplay(t *testing.T) {
	s, c, call := sessionHarness(t)
	s.Agent.Cfg.IdleSec = 71
	s.Agent.Cfg.AbsSec = 193
	ctx := context.Background()
	a := s.Agent
	seal, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	a.Secret = seal
	u, e := a.Store.UserByName(ctx, "reader")
	if e != nil {
		t.Fatal(e)
	}
	key, _, e := auth.GenerateTOTP("reader")
	if e != nil {
		t.Fatal(e)
	}
	sealed, e := seal.Seal([]byte(key))
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(sealed)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Store.SetPendingMFA(ctx, u.ID, string(b)); e != nil {
		t.Fatal(e)
	}
	code, e := totp.GenerateCode(key, time.UnixMilli(c.now))
	if e != nil {
		t.Fatal(e)
	}
	step, ok := auth.VerifyTOTP(key, code, time.UnixMilli(c.now))
	if !ok {
		t.Fatal("code")
	}
	codes := make([]store.BackupCode, 8)
	for i := range codes {
		codes[i] = store.BackupCode{ID: sessionID(t), UserID: u.ID, CodePHC: auth.Hash("backup-proof" + string(rune('a'+i)))}
	}
	if e = a.Store.ConfirmMFA(ctx, u.ID, string(b), step, 600000, codes); e != nil {
		t.Fatal(e)
	}
	c.now += 30000
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	if r := call("session.login", anon, `{"user":"reader","password":"correct password"}`); r.Code != "unauthenticated" {
		t.Fatalf("MFA omitted %+v", r)
	}
	code, e = totp.GenerateCode(key, time.UnixMilli(c.now))
	if e != nil {
		t.Fatal(e)
	}
	args, _ := json.Marshal(map[string]string{"user": "reader", "password": "correct password", "totp": code})
	login := sessionData(t, call("session.login", anon, string(args)))
	token := login["session"].(string)
	if login["mfa"] != true {
		t.Fatalf("MFA %+v", login)
	}
	proof, _ := json.Marshal(map[string]string{"totp": code})
	if r := call("session.reauth", token, string(proof)); r.Code != "unauthenticated" {
		t.Fatalf("replay %+v", r)
	}
	before, e := a.Store.GetSession(ctx, strings.Split(token, "_")[1])
	if e != nil {
		t.Fatal(e)
	}
	c.now += 1000
	reauth := sessionData(t, call("session.reauth", token, `{"totp":"backup-proofa"}`))
	fresh := reauth["session"].(string)
	if fresh == token || reauth["csrf_token"] == login["csrf_token"] || reauth["mfa_at"] != float64(c.now) {
		t.Fatalf("rotation %+v", reauth)
	}
	rotated, e := a.Store.GetSession(ctx, strings.Split(fresh, "_")[1])
	if e != nil || rotated.IdleDeadline != c.now+71000 || rotated.AbsDeadline != before.AbsDeadline {
		t.Fatal("reauth must use configured idle without extending absolute lifetime")
	}
	if r := call("session.whoami", token, `{}`); r.Code != "unauthenticated" {
		t.Fatalf("old %+v", r)
	}
	if r := call("session.reauth", fresh, `{"totp":"backup-proofa"}`); r.Code != "unauthenticated" {
		t.Fatalf("backup reuse %+v", r)
	}
}

func TestRealPATResolverAndSelfServiceRefusal(t *testing.T) {
	s, _, call := sessionHarness(t)
	ctx := context.Background()
	u, e := s.Agent.Store.UserByName(ctx, "reader")
	if e != nil {
		t.Fatal(e)
	}
	tid := sessionID(t)
	secret := strings.Repeat("a", 64)
	token := "npat_" + tid + "_" + secret
	if e = s.Agent.Store.CreateAPIToken(ctx, store.APIToken{ID: tid, Name: "monitor", SecretPHC: auth.Hash(secret), Scopes: []string{"system:read"}, CreatedBy: u.ID}); e != nil {
		t.Fatal(e)
	}
	c, e := s.Sessions.Resolve(ctx, token)
	if e != nil || c.Kind != "token" || c.Session != tid || c.Automation {
		t.Fatalf("resolve %+v %v", c, e)
	}
	for _, verb := range []string{"session.logout", "session.reauth", "session.whoami", "session.start", "session.login", "mfa.enroll", "mfa.confirm", "mfa.disable", "tokens.create", "tokens.list", "tokens.revoke", "user.list", "user.create", "user.update", "user.delete", "user.set_password"} {
		want := "forbidden"
		if verb == "session.start" {
			want = "precondition_failed"
		}
		if r := call(verb, token, `{}`); r.Code != want {
			t.Fatalf("token %s %+v", verb, r)
		}
	}
	if e = s.Agent.Store.RevokeAPIToken(ctx, tid); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Sessions.Resolve(ctx, token); e == nil {
		t.Fatal("revoked token resolved")
	}
}
func TestLoginRehashesWeakPassword(t *testing.T) {
	s, _, call := sessionHarness(t)
	ctx := context.Background()
	u, e := s.Agent.Store.UserByName(ctx, "reader")
	if e != nil {
		t.Fatal(e)
	}
	weak := "$argon2id$v=19$m=8192,t=1,p=1$KioqKioqKioqKioqKioqKg$yj601DUth0a4EptCqIumdHhvbbS9VSmIznW3AoXb2n4"
	if e = s.Agent.Store.SetPassword(ctx, u.ID, weak); e != nil {
		t.Fatal(e)
	}
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	sessionData(t, call("session.login", anon, `{"user":"reader","password":"known"}`))
	u, e = s.Agent.Store.UserByID(ctx, u.ID)
	if e != nil {
		t.Fatal(e)
	}
	if ok, rehash := auth.Verify("known", u.PWPHC); !ok || rehash || u.PWPHC == weak {
		t.Fatal("not rehashed")
	}
}

func TestLoginRejectsAlreadyAuthenticatedSession(t *testing.T) {
	_, _, call := sessionHarness(t)
	if r := call("session.login", "", `{"user":"reader","password":"correct password"}`); r.Code != "unauthenticated" {
		t.Fatalf("no anonymous bootstrap %+v", r)
	}
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	if r := call("session.login", token, `{"user":"reader","password":"correct password"}`); r.Code != "unauthenticated" {
		t.Fatalf("user login %+v", r)
	}
	sessionData(t, call("session.whoami", token, `{}`))
}
func TestConfiguredAuthenticationBudgets(t *testing.T) {
	s, c, call := sessionHarness(t)
	s.Agent.Cfg.LoginMaxAttempts = 2
	s.Agent.Cfg.LoginWindowSec = 900
	s.Agent.Cfg.TOTPMaxAttempts = 1
	anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
	for i := 0; i < 2; i++ {
		if r := call("session.login", anon, `{"user":"reader","password":"wrong"}`); r.Code != "unauthenticated" {
			t.Fatalf("early limit %+v", r)
		}
	}
	if r := call("session.login", anon, `{"user":"reader","password":"correct password"}`); r.Code != "unauthenticated" {
		t.Fatalf("configured budget ignored %+v", r)
	}
	c.now += 60000
	if r := call("session.login", anon, `{"user":"reader","password":"wrong"}`); r.Code != "unauthenticated" {
		t.Fatalf("configured window ignored %+v", r)
	}
	c.now += 840000
	anon = sessionData(t, call("session.start", "", `{}`))["session"].(string)
	token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
	if r := call("mfa.confirm", token, `{"totp":"invalid"}`); r.Code != "unauthenticated" {
		t.Fatalf("first proof %+v", r)
	}
	if r := call("mfa.confirm", token, `{"totp":"invalid"}`); r.Code != "unauthenticated" {
		t.Fatalf("configured MFA budget ignored %+v", r)
	}
}

func TestDurableAuditFailurePreventsSessionMint(t *testing.T) {
	s, _, call := sessionHarness(t)
	s.DurableAudit = func(rpc.AuditEvent) error { return context.Canceled }
	if r := call("session.start", "", `{}`); r.Code != "agent_error" {
		t.Fatalf("audit fail %+v", r)
	}
}

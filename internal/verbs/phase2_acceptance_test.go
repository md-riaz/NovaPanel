package verbs_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/auth"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

// These acceptance checks use the production dispatcher, SQLite, file audit and
// mirror, not an AuditFunc recorder. They never initialize privileged services.
type phase2AuditHarness struct {
	t         *testing.T
	s         *rpc.Server
	clock     *sessionClock
	path      string
	forbidden []string
}

func newPhase2AuditHarness(t *testing.T) *phase2AuditHarness {
	t.Helper()
	dir := t.TempDir()
	clock := &sessionClock{now: 1800000000000}
	st, err := store.OpenWithClock(filepath.Join(dir, "panel.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	path := filepath.Join(dir, "audit.jsonl")
	log, err := audit.Open(st, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	a := agent.New()
	a.Store, a.Audit, a.Clock = st, log, clock
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := rpc.NewServer(a)
	s.WebUID = 1001
	return &phase2AuditHarness{t: t, s: s, clock: clock, path: path}
}

func phase2JSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (h *phase2AuditHarness) records() []map[string]any {
	h.t.Helper()
	b, err := os.ReadFile(h.path)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, value := range append(h.forbidden, "$argon2id$", "pw_phc", "secret_phc", "token_hash", "totp_secret", "totp_pending", "backup_codes", "csrf_token") {
		if value != "" && strings.Contains(string(b), value) {
			h.t.Fatal("secret material in durable audit")
		}
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// Check the exact per-request admission/completion sequence in both independent
// durable sinks. Actor is an untrusted label by contract, not authenticated identity.
func (h *phase2AuditHarness) call(name, verb, token string, root bool, args any, code, role, stage string) rpc.Reply {
	h.t.Helper()
	before := len(h.records())
	actor := "gate8/" + name
	caller := rpc.Caller{Peer: 1001}
	if root {
		caller = rpc.RootCaller(0)
		caller.MFAat = h.clock.now
	}
	expected := map[string]any{"kind": "unresolved"}
	if root {
		expected = map[string]any{"kind": "local-root"}
	} else if token != "" {
		resolved, err := h.s.Sessions.Resolve(context.Background(), token)
		if err == nil {
			expected = map[string]any{"kind": resolved.Kind}
			if resolved.Kind == "token" {
				expected["token_id"] = resolved.Session
			} else {
				expected["session_id"] = resolved.Session
				if resolved.UserID != "" {
					expected["user_id"] = resolved.UserID
				}
			}
		}
	}
	reply := h.s.Dispatch(rpc.WithCaller(context.Background(), caller), rpc.Request{ID: sessionID(h.t), Verb: verb, Session: token, Actor: actor, Args: phase2JSON(h.t, args)})
	if reply.Code != code || reply.OK != (code == "") {
		h.t.Fatalf("%s: code=%q ok=%v; want code=%q", name, reply.Code, reply.OK, code)
	}
	outcome := "ok"
	if code != "" {
		outcome = "refused"
	}
	if code == "agent_error" {
		outcome = "error"
	}
	records := h.records()
	delta := records[before:]
	want := 1
	if stage == "exec" {
		want = 2
	}
	if len(delta) != want {
		h.t.Fatalf("%s: got %d audit records, want %d", name, len(delta), want)
	}
	rows, err := h.s.Agent.Store.ListEvents(context.Background(), 1000, "")
	if err != nil {
		h.t.Fatal(err)
	}
	for i, record := range delta {
		wantStage, wantOutcome, wantCode := stage, outcome, code
		if want == 2 && i == 0 {
			wantStage, wantOutcome, wantCode = "authz", "ok", ""
		}
		if record["actor"] != actor || record["role"] != role || record["verb"] != verb || record["outcome"] != wantOutcome || record["code"] != wantCode || record["object_key"] != "" {
			h.t.Fatalf("%s: wrong audit attribution/outcome: %+v", name, record)
		}
		detail, ok := record["detail"].(map[string]any)
		if !ok || !reflect.DeepEqual(detail, map[string]any{"stage": wantStage, "principal": expected}) {
			h.t.Fatalf("%s: unexpected detail: %+v", name, record["detail"])
		}
		if len(record) != 9 || record["ts"].(float64) <= 0 {
			h.t.Fatalf("%s: invalid durable envelope", name)
		}
		found := false
		for _, row := range rows {
			if row.ID != record["id"] {
				continue
			}
			found = true
			var dbDetail map[string]any
			if err := json.Unmarshal(row.Detail, &dbDetail); err != nil {
				h.t.Fatal(err)
			}
			if row.Actor != actor || row.Role != role || row.Verb != verb || row.Outcome != wantOutcome || row.Code != wantCode || row.ObjectKey != "" || float64(row.TS) != record["ts"] || !reflect.DeepEqual(dbDetail, detail) {
				h.t.Fatalf("%s: file/SQLite disagreement", name)
			}
		}
		if !found {
			h.t.Fatalf("%s: no matching SQLite mirror row", name)
		}
	}
	return reply
}

func (h *phase2AuditHarness) start(name string) string {
	return sessionData(h.t, h.call(name, "session.start", "", false, map[string]any{}, "", "", "exec"))["session"].(string)
}
func (h *phase2AuditHarness) login(name, username, password string) string {
	anon := h.start(name + "-start")
	h.forbidden = append(h.forbidden, anon, password)
	token := sessionData(h.t, h.call(name, "session.login", anon, false, map[string]any{"user": username, "password": password}, "", "", "exec"))["session"].(string)
	h.forbidden = append(h.forbidden, token)
	h.records()
	return token
}

func TestPhase2Gate8BudgetRefusedLoginAudit(t *testing.T) {
	h := newPhase2AuditHarness(t)
	h.s.Agent.Cfg.LoginMaxAttempts = 1
	h.call("budget-user", "user.create", "", true, map[string]any{"username": "reader", "email": "reader@example.test", "role": "read_only", "password": "gate8-reader-password"}, "", "owner", "exec")
	anon := h.start("budget-start")
	h.forbidden = append(h.forbidden, anon, "gate8-reader-password", "gate8-wrong-password")
	h.call("wrong-password", "session.login", anon, false, map[string]any{"user": "reader", "password": "gate8-wrong-password"}, "unauthenticated", "", "exec")
	h.call("budget-exhausted", "session.login", anon, false, map[string]any{"user": "reader", "password": "gate8-reader-password"}, "unauthenticated", "", "exec")
	h.clock.now += 900000
	token := h.login("budget-window-recovered", "reader", "gate8-reader-password")
	h.call("read-only-write", "tokens.revoke", token, false, map[string]any{"token_id": sessionID(t)}, "forbidden", "read_only", "authz")
	h.call("read-only-logout", "session.logout", token, false, map[string]any{}, "", "read_only", "exec")
}

func TestPhase2Gate8UserPrivilegeChangeAudit(t *testing.T) {
	h := newPhase2AuditHarness(t)
	ctx := context.Background()
	created := sessionData(t, h.call("create-admin", "user.create", "", true, map[string]any{"username": "operator", "email": "op@example.test", "role": "admin", "password": "gate8-initial-password"}, "", "owner", "exec"))
	uid := created["user_id"].(string)
	h.forbidden = append(h.forbidden, "gate8-initial-password", "gate8-reset-password")
	token := h.login("login-admin", "operator", "gate8-initial-password")
	cases := []struct {
		name, verb string
		args       map[string]any
	}{
		{"create", "user.create", map[string]any{"username": "newuser", "email": "new@example.test", "role": "read_only", "password": "gate8-initial-password"}},
		{"role", "user.update", map[string]any{"user_id": uid, "role": "read_only"}},
		{"promote", "user.update", map[string]any{"user_id": uid, "role": "owner"}},
		{"email", "user.update", map[string]any{"user_id": uid, "email": "updated@example.test"}},
		{"disable", "user.update", map[string]any{"user_id": uid, "disabled": true}},
		{"enable", "user.update", map[string]any{"user_id": uid, "disabled": false}},
		{"password-reset", "user.set_password", map[string]any{"user_id": uid, "password": "gate8-reset-password"}},
		{"delete", "user.delete", map[string]any{"user_id": uid}},
	}
	// Every canonical user privilege-change class is denied to admin before execution.
	for _, tc := range cases {
		t.Run("admin-refused-"+tc.name, func(t *testing.T) {
			h.call("admin-refused-"+tc.name, tc.verb, token, false, tc.args, "forbidden", "admin", "authz")
		})
	}
	// Use a separate target for create, and keep the operator lifecycle explicit.
	for _, tc := range cases {
		t.Run("owner-"+tc.name, func(t *testing.T) {
			h.call("owner-"+tc.name, tc.verb, "", true, tc.args, "", "owner", "exec")
			if tc.name == "create" {
				return
			}
			u, err := h.s.Agent.Store.UserByID(ctx, uid)
			if tc.name == "delete" {
				if err == nil {
					t.Fatal("deleted user remains")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch tc.name {
			case "role":
				if u.Role != "read_only" {
					t.Fatal("role not persisted")
				}
			case "promote":
				if u.Role != "owner" {
					t.Fatal("promotion not persisted")
				}
			case "email":
				if u.Email != "updated@example.test" {
					t.Fatal("email not persisted")
				}
			case "disable":
				if !u.Disabled {
					t.Fatal("disable not persisted")
				}
			case "enable":
				if u.Disabled {
					t.Fatal("enable not persisted")
				}
			case "password-reset":
				if ok, _ := auth.Verify("gate8-reset-password", u.PWPHC); !ok {
					t.Fatal("reset not persisted")
				}
			}
			h.forbidden = append(h.forbidden, u.PWPHC)
		})
	}
	h.call("deleted-user", "user.delete", "", true, map[string]any{"user_id": uid}, "not_found", "owner", "exec")
	h.call("invalid-role", "user.update", "", true, map[string]any{"user_id": uid, "role": "root"}, "invalid_argument", "owner", "authz")
	h.call("revoked-by-role-change", "session.logout", token, false, map[string]any{}, "unauthenticated", "", "authz")
	h.records()
}

func TestPhase2Gate8SessionMFATokenPrivilegeChangeAudit(t *testing.T) {
	h := newPhase2AuditHarness(t)
	ctx := context.Background()
	h.call("create-owner", "user.create", "", true, map[string]any{"username": "owner", "email": "owner@example.test", "role": "owner", "password": "gate8-owner-password"}, "", "owner", "exec")
	anon := h.start("failed-login-start")
	h.forbidden = append(h.forbidden, anon, "gate8-wrong-password")
	for _, username := range []string{"owner", "unknown"} {
		h.call("bad-login-"+username, "session.login", anon, false, map[string]any{"user": username, "password": "gate8-wrong-password"}, "unauthenticated", "", "exec")
	}
	token := h.login("owner-login", "owner", "gate8-owner-password")
	h.call("no-stepup-pat", "tokens.create", token, false, map[string]any{"name": "monitor", "scopes": []string{"system:read"}}, "forbidden", "owner", "authz")
	h.call("no-stepup-disable", "mfa.disable", token, false, map[string]any{"totp": "invalid"}, "forbidden", "owner", "authz")
	h.call("bad-reauth", "session.reauth", token, false, map[string]any{"totp": "invalid"}, "unauthenticated", "owner", "exec")
	// A real handler error, not a fabricated audit event or replacement dispatcher.
	sealer := h.s.Agent.Secret
	h.s.Agent.Secret = nil
	h.call("enroll-unavailable", "mfa.enroll", token, false, map[string]any{}, "agent_error", "owner", "exec")
	h.s.Agent.Secret = sealer
	enroll := sessionData(t, h.call("enroll", "mfa.enroll", token, false, map[string]any{}, "", "owner", "exec"))
	uri, err := url.Parse(enroll["otpauth_uri"].(string))
	if err != nil {
		t.Fatal(err)
	}
	key := uri.Query().Get("secret")
	h.forbidden = append(h.forbidden, key, uri.String())
	h.call("bad-confirm", "mfa.confirm", token, false, map[string]any{"totp": "invalid"}, "unauthenticated", "owner", "exec")
	proof, err := totp.GenerateCode(key, time.UnixMilli(h.clock.now))
	if err != nil {
		t.Fatal(err)
	}
	h.forbidden = append(h.forbidden, proof)
	confirm := sessionData(t, h.call("confirm", "mfa.confirm", token, false, map[string]any{"totp": proof}, "", "owner", "exec"))
	codes := confirm["backup_codes"].([]any)
	for _, v := range codes {
		h.forbidden = append(h.forbidden, v.(string))
	}
	u, err := h.s.Agent.Store.UserByName(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if u.TOTPSecret == nil || u.TOTPPending != nil {
		t.Fatal("MFA activation not committed")
	}
	h.forbidden = append(h.forbidden, *u.TOTPSecret, u.PWPHC)
	backupRows, err := h.s.Agent.Store.ListBackupCodes(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range backupRows {
		h.forbidden = append(h.forbidden, row.CodePHC)
	}
	h.call("enroll-active", "mfa.enroll", token, false, map[string]any{}, "conflict", "owner", "exec")
	mfaAnon := h.start("mfa-login-start")
	h.forbidden = append(h.forbidden, mfaAnon)
	h.call("missing-factor-login", "session.login", mfaAnon, false, map[string]any{"user": "owner", "password": "gate8-owner-password"}, "unauthenticated", "", "exec")
	mfaToken := sessionData(t, h.call("mfa-login", "session.login", mfaAnon, false, map[string]any{"user": "owner", "password": "gate8-owner-password", "totp": codes[2]}, "", "", "exec"))["session"].(string)
	h.forbidden = append(h.forbidden, mfaToken)
	h.call("mfa-login-logout", "session.logout", mfaToken, false, map[string]any{}, "", "owner", "exec")
	fresh := sessionData(t, h.call("reauth", "session.reauth", token, false, map[string]any{"totp": codes[0]}, "", "owner", "exec"))["session"].(string)
	if fresh == token {
		t.Fatal("reauth did not rotate")
	}
	h.forbidden = append(h.forbidden, fresh)
	h.call("old-session", "session.logout", token, false, map[string]any{}, "unauthenticated", "", "authz")
	pat := sessionData(t, h.call("pat-create", "tokens.create", fresh, false, map[string]any{"name": "monitor", "scopes": []string{"system:read"}, "automation": false}, "", "owner", "exec"))["token"].(string)
	h.forbidden = append(h.forbidden, pat, strings.Split(pat, "_")[2])
	tid := strings.Split(pat, "_")[1]
	patRow, err := h.s.Agent.Store.GetAPIToken(ctx, tid)
	if err != nil {
		t.Fatal(err)
	}
	h.forbidden = append(h.forbidden, patRow.SecretPHC)
	if _, err := h.s.Sessions.Resolve(ctx, pat); err != nil {
		t.Fatal(err)
	}
	h.call("pat-self-service", "session.logout", pat, false, map[string]any{}, "forbidden", "", "authz")
	h.call("pat-revoke", "tokens.revoke", fresh, false, map[string]any{"token_id": tid}, "", "owner", "exec")
	if _, err := h.s.Sessions.Resolve(ctx, pat); err == nil {
		t.Fatal("revoked PAT resolved")
	}
	h.call("backup-replay-disable", "mfa.disable", fresh, false, map[string]any{"totp": codes[0]}, "unauthenticated", "owner", "exec")
	h.call("disable", "mfa.disable", fresh, false, map[string]any{"totp": codes[1]}, "", "owner", "exec")
	u, err = h.s.Agent.Store.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.TOTPSecret != nil {
		t.Fatal("disable not persisted")
	}
	// Disable invalidates all user sessions; logout is verified with a new login.
	token = h.login("post-disable-login", "owner", "gate8-owner-password")
	h.call("logout", "session.logout", token, false, map[string]any{}, "", "owner", "exec")
	if _, err := h.s.Sessions.Resolve(ctx, token); err == nil {
		t.Fatal("logged-out session resolved")
	}
	h.records()
}

package verbs_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"novapanel/internal/audit"
	"novapanel/internal/auth"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
)

type authAuditSink struct {
	bytes.Buffer
	fail bool
}

func (s *authAuditSink) Write(p []byte) (int, error) {
	if s.fail {
		return 0, errors.New("private sink error")
	}
	return s.Buffer.Write(p)
}
func (*authAuditSink) Sync() error  { return nil }
func (*authAuditSink) Close() error { return nil }

func TestCommittedAuthReplySurvivesCompletionAuditFailure(t *testing.T) {
	for _, verb := range []string{"mfa.confirm", "tokens.create"} {
		for _, sinkName := range []string{"file", "events"} {
			for _, attemptFailure := range []bool{false, true} {
				t.Run(verb+"/"+sinkName+map[bool]string{true: "/attempt", false: "/completion"}[attemptFailure], func(t *testing.T) {
					s, c, call := sessionHarness(t)
					ctx := context.Background()
					anon := sessionData(t, call("session.start", "", `{}`))["session"].(string)
					token := sessionData(t, call("session.login", anon, `{"user":"reader","password":"correct password"}`))["session"].(string)
					seal, e := secret.New(make([]byte, 32))
					if e != nil {
						t.Fatal(e)
					}
					s.Agent.Secret = seal
					args := `{"name":"test","scopes":["system:read"]}`
					if verb == "mfa.confirm" {
						uri := sessionData(t, call("mfa.enroll", token, `{}`))["otpauth_uri"].(string)
						u, e := url.Parse(uri)
						if e != nil {
							t.Fatal(e)
						}
						code, e := totp.GenerateCode(u.Query().Get("secret"), time.UnixMilli(c.now))
						if e != nil {
							t.Fatal(e)
						}
						b, _ := json.Marshal(map[string]string{"totp": code})
						args = string(b)
					} else {
						token = ""
					}
					sink := &authAuditSink{}
					auditLog := audit.New(s.Agent.Store, sink)
					defer auditLog.Close()
					var logs bytes.Buffer
					old := log.Writer()
					log.SetOutput(&logs)
					defer log.SetOutput(old)
					calls := 0
					s.DurableAudit = func(ev rpc.AuditEvent) error {
						calls++
						if calls == map[bool]int{true: 1, false: 2}[attemptFailure] {
							if sinkName == "file" {
								sink.fail = true
							} else {
								if e := s.Agent.Store.Tx(ctx, func(tx *sql.Tx) error {
									_, e := tx.Exec(`CREATE TRIGGER reject_events BEFORE INSERT ON events BEGIN SELECT RAISE(FAIL,'private sink error'); END`)
									return e
								}); e != nil {
									t.Fatal(e)
								}
							}
						}
						return auditLog.Record(audit.Event{Actor: ev.Actor, Role: ev.Role, Verb: ev.Verb, Outcome: ev.Outcome, Code: ev.Code, Detail: ev.Detail})
					}
					var r rpc.Reply
					if verb == "tokens.create" {
						r = s.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Peer: 1001, Kind: "user", UserID: func() string {
							u, e := s.Agent.Store.UserByName(ctx, "reader")
							if e != nil {
								t.Fatal(e)
							}
							return u.ID
						}(), Session: "verified", Role: "owner", MFAat: c.now}), rpc.Request{ID: "once", Verb: verb, Args: []byte(args)})
					} else {
						r = call(verb, token, args)
					}
					var count int
					table := "backup_codes"
					if verb == "tokens.create" {
						table = "api_tokens"
					}
					if e := s.Agent.Store.Tx(ctx, func(tx *sql.Tx) error { return tx.QueryRow("SELECT count(*) FROM " + table).Scan(&count) }); e != nil {
						t.Fatal(e)
					}
					if attemptFailure {
						if r.Code != "agent_error" || r.Data != nil || count != 0 {
							t.Fatalf("attempt effect: %+v count=%d", r, count)
						}
						return
					}
					data := sessionData(t, r)
					secrets := []string{}
					if verb == "tokens.create" {
						secrets = append(secrets, data["token"].(string))
						if count != 1 {
							t.Fatal(count)
						}
					} else {
						for _, v := range data["backup_codes"].([]any) {
							secrets = append(secrets, v.(string))
						}
						if count != 8 {
							t.Fatal(count)
						}
					}
					if auditLog.Healthy() == nil {
						t.Fatal("health not sticky")
					}
					if e := s.Agent.Store.Tx(ctx, func(tx *sql.Tx) error {
						return tx.QueryRow("SELECT count(*) FROM events WHERE verb=?", verb).Scan(&count)
					}); e != nil {
						t.Fatal(e)
					}
					if count != 1 {
						t.Fatalf("attempt rows=%d", count)
					}
					sink.fail = false
					if sinkName == "events" {
						if e := s.Agent.Store.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DROP TRIGGER reject_events"); return e }); e != nil {
							t.Fatal(e)
						}
					}
					if next := call("session.logout", token, `{}`); verb == "mfa.confirm" && next.Code != "agent_error" {
						t.Fatalf("mutation admitted %+v", next)
					}
					if next := s.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Peer: 1001, Kind: "user", UserID: func() string {
						u, e := s.Agent.Store.UserByName(ctx, "reader")
						if e != nil {
							t.Fatal(e)
						}
						return u.ID
					}(), Session: "verified", Role: "owner", MFAat: c.now}), rpc.Request{Verb: "user.create", Args: []byte(`{"username":"later","password":"password","role":"admin","email":"later@example.test"}`)}); next.Code != "agent_error" {
						t.Fatalf("mutation admitted %+v", next)
					}
					for _, v := range secrets {
						if strings.Contains(sink.String()+logs.String(), v) {
							t.Fatal("secret leaked")
						}
					}
					if strings.Contains(logs.String(), "private sink error") {
						t.Fatal("sink error leaked")
					}
					_ = auth.Verify
				})
			}
		}
	}
}

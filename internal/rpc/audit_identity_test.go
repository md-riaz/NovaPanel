package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type identityResolver struct {
	caller Caller
	err    error
}

func (r identityResolver) Resolve(context.Context, string) (Caller, error) { return r.caller, r.err }

func TestDurableResolvedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		caller      Caller
		raw, code   string
		failResolve bool
		principal   map[string]any
	}{
		{"user", Caller{Kind: "user", Role: "admin", UserID: "user-id", Session: "session-id"}, `{}`, "", false, map[string]any{"kind": "user", "user_id": "user-id", "session_id": "session-id"}},
		{"malformed", Caller{Kind: "user", Role: "admin", UserID: "user-id", Session: "session-id"}, `[]`, "bad_request", false, map[string]any{"kind": "user", "user_id": "user-id", "session_id": "session-id"}},
		{"unknown_field", Caller{Kind: "user", Role: "admin", UserID: "user-id", Session: "session-id"}, `{"principal":{"user_id":"forged"}}`, "bad_request", false, map[string]any{"kind": "user", "user_id": "user-id", "session_id": "session-id"}},
		{"denied", Caller{Kind: "user", Role: "read_only", UserID: "user-id", Session: "session-id"}, `{}`, "forbidden", false, map[string]any{"kind": "user", "user_id": "user-id", "session_id": "session-id"}},
		{"token", Caller{Kind: "token", Session: "token-id", Scopes: []string{"system:write"}}, `{}`, "", false, map[string]any{"kind": "token", "token_id": "token-id"}},
		{"anonymous", Caller{Kind: "anon", Session: "anon-id"}, `{}`, "unauthenticated", false, map[string]any{"kind": "anon", "session_id": "anon-id"}},
		{"unresolved", Caller{}, `{}`, "unauthenticated", true, map[string]any{"kind": "unresolved"}},
		{"root", RootCaller(0), `{}`, "", false, map[string]any{"kind": "local-root"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			st, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			path := filepath.Join(dir, "audit.log")
			l, err := audit.Open(st, path)
			if err != nil {
				t.Fatal(err)
			}
			a := agent.New()
			a.Store = st
			a.Audit = l
			s := NewServer(a)
			s.Sessions = identityResolver{caller: tc.caller}
			if tc.failResolve {
				s.Sessions = identityResolver{err: errors.New("denied")}
			}
			s.Registry = map[string]Verb{"test": {Name: "test", MinRole: Admin, Scope: "system:write", NewArgs: func() any { return &struct{}{} }, ResolveObjectKey: func(context.Context, *agent.Agent, any) (string, error) { return "site:persisted-parent", nil }, Run: func(context.Context, *agent.Agent, any) (any, error) { return nil, nil }}}
			caller := Caller{Peer: 42}
			if tc.name == "root" {
				caller = tc.caller
			}
			reply := s.Dispatch(WithCaller(ctx, caller), Request{Verb: "test", Actor: `forged user_id=attacker session_id=attacker`, Session: "nps_presented_secret", Args: json.RawMessage(tc.raw)})
			if reply.Code != tc.code {
				t.Fatalf("reply %+v", reply)
			}
			events, err := st.ListEvents(ctx, 20, "")
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if tc.code == "" {
				wantCount = 2
			}
			if len(events) != wantCount {
				t.Fatalf("events %d want %d", len(events), wantCount)
			}
			if err = l.Close(); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fileEvents []map[string]any
			for _, line := range splitAuditLines(b) {
				var ev map[string]any
				if err = json.Unmarshal(line, &ev); err != nil {
					t.Fatal(err)
				}
				fileEvents = append(fileEvents, ev)
			}
			for _, ev := range events {
				var detail map[string]any
				if err = json.Unmarshal(ev.Detail, &detail); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(detail["principal"], tc.principal) {
					t.Fatalf("principal=%#v want=%#v", detail["principal"], tc.principal)
				}
				if tc.code == "" && ev.ObjectKey != "site:persisted-parent" {
					t.Fatalf("object %q", ev.ObjectKey)
				}
				found := false
				for _, f := range fileEvents {
					if f["id"] == ev.ID {
						found = true
						if !reflect.DeepEqual(f["detail"], detail) || f["actor"] != ev.Actor {
							t.Fatal("mirror mismatch")
						}
					}
				}
				if !found {
					t.Fatal("missing mirror")
				}
			}
			// Replay into a fresh SQLite mirror, not just a no-op reopen.
			mirror, err := store.Open(filepath.Join(dir, "replay.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer mirror.Close()
			replay, err := audit.Open(mirror, path)
			if err != nil {
				t.Fatal(err)
			}
			defer replay.Close()
			restored, err := mirror.ListEvents(ctx, 20, "")
			if err != nil || !reflect.DeepEqual(events, restored) {
				t.Fatalf("replay mismatch %v", err)
			}
		})
	}
}

func splitAuditLines(b []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			lines = append(lines, b[start:i])
			start = i + 1
		}
	}
	return lines
}

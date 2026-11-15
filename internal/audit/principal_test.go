package audit

import (
	"context"
	"encoding/json"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

func TestJobPrincipal(t *testing.T) {
	for _, tc := range []struct {
		name, createdBy, role string
		scopes                []string
		want                  Principal
	}{
		{"legacy-token", "token-id", "", []string{"system:write"}, Principal{Kind: "token", TokenID: "token-id"}},
		{"explicit-token", "token-id", "token", nil, Principal{Kind: "token", TokenID: "token-id"}},
		{"user", "session-id", "admin", nil, Principal{Kind: "user", SessionID: "session-id"}},
		{"user-scopes-do-not-imply-token", "session-id", "admin", []string{"system:write"}, Principal{Kind: "user", SessionID: "session-id"}},
		{"root-precedence", "local-root", "token", []string{"*"}, Principal{Kind: "local-root"}},
		{"legacy-root-precedence", "local-root", "", []string{"*"}, Principal{Kind: "local-root"}},
		{"missing-identity", "", "token", []string{"*"}, Principal{Kind: "unresolved"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := JobPrincipal(tc.createdBy, tc.role, tc.scopes); got != tc.want {
				t.Fatalf("principal %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestPrincipalPreservesOnlyTrustedIDs(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sink := &fakeSink{}
	l := New(s, sink)
	for _, p := range []Principal{{Kind: "token", TokenID: "row-id"}, {Kind: "token", TokenID: "npat_private_credential"}} {
		if err = l.Record(Event{Principal: p, Outcome: "ok", Detail: map[string]any{"principal": map[string]any{"kind": "forged", "token_id": "attacker"}, "token_id": "raw-secret", "password": "password-secret"}}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListEvents(context.Background(), 10, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, ev := range events {
		var d map[string]any
		if err = json.Unmarshal(ev.Detail, &d); err != nil {
			t.Fatal(err)
		}
		p := d["principal"].(map[string]any)
		seen[p["token_id"].(string)] = true
		if p["kind"] != "token" || d["token_id"] != "[REDACTED]" || d["password"] != "[REDACTED]" {
			t.Fatalf("projection/redaction %#v", d)
		}
	}
	if !seen["row-id"] || !seen["[REDACTED]"] {
		t.Fatal(seen)
	}
}

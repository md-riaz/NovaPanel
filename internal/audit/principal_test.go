package audit

import (
	"context"
	"encoding/json"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

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

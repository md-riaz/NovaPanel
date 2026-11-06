package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/dns"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"strings"
	"testing"

	"novapanel/internal/rpc"
)

func TestIntegrationSetResolvedSessions(t *testing.T) {
	h := newPhase2AuditHarness(t)
	h.call("integration-admin", "user.create", "", true, map[string]any{"username": "probeadmin", "email": "probe@example.test", "role": "admin", "password": "synthetic-login-password"}, "", "owner", "exec")
	token := h.login("integration-login", "probeadmin", "synthetic-login-password")
	args := map[string]any{"kind": "cloudflare", "token": "synthetic-scope-token", "zones": []string{"example.com"}}
	h.call("integration-unstepped", "integration.set", token, false, args, "forbidden", "admin", "authz")
	resolved, e := h.s.Sessions.Resolve(context.Background(), token)
	if e != nil {
		t.Fatal(e)
	}
	if e = h.s.Agent.Store.Tx(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec("UPDATE sessions SET mfa_at=? WHERE id=?", h.clock.now, resolved.Session)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	h.call("integration-stepped", "integration.set", token, false, args, "unsupported", "admin", "exec")
	for _, automation := range []bool{false, true} {
		reply := h.call("integration-mint-PAT", "tokens.create", token, false, map[string]any{"name": "probe", "scopes": []string{"dns:write"}, "automation": automation}, "", "admin", "exec")
		pat := sessionData(t, reply)["token"].(string)
		h.forbidden = append(h.forbidden, pat)
		h.call("integration-PAT-refused", "integration.set", pat, false, args, "forbidden", "", "authz")
	}
	h.forbidden = append(h.forbidden, "synthetic-scope-token")
	h.records()
}

func TestIntegrationSetDispatcherAuthorityAndShape(t *testing.T) {
	h := newPhase2AuditHarness(t)
	args := json.RawMessage(`{"kind":"cloudflare","token":"synthetic-scope-token","zones":["example.com"]}`)
	for _, tc := range []struct {
		name   string
		caller rpc.Caller
		args   json.RawMessage
		code   string
	}{
		{"anonymous", rpc.Caller{Peer: 1001}, args, "unauthenticated"},
		{"reader", rpc.Caller{Peer: 1001, Kind: "user", Role: "read_only", UserID: "reader"}, args, "forbidden"},
		{"admin needs MFA", rpc.Caller{Peer: 1001, Kind: "user", Role: "admin", UserID: "admin"}, args, "forbidden"},
		{"PAT no scope even automation", rpc.Caller{Peer: 1001, Kind: "token", Scopes: []string{"*"}, Automation: true}, args, "forbidden"},
		{"root no probe", rpc.RootCaller(0), args, "unsupported"},
		{"unknown field", rpc.RootCaller(0), json.RawMessage(`{"kind":"cloudflare","token":"synthetic-scope-token","zones":["example.com"],"user_id":"owner"}`), "bad_request"},
		{"bad token", rpc.RootCaller(0), json.RawMessage(`{"kind":"cloudflare","token":"white space","zones":["example.com"]}`), "invalid_argument"},
		{"uppercase zone", rpc.RootCaller(0), json.RawMessage(`{"kind":"cloudflare","token":"synthetic-scope-token","zones":["EXAMPLE.com"]}`), "invalid_argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.caller
			if c.Role == "owner" {
				c.MFAat = h.clock.now
			}
			reply := h.s.Dispatch(rpc.WithCaller(context.Background(), c), rpc.Request{Verb: "integration.set", Args: tc.args})
			if reply.OK || reply.Code != tc.code {
				t.Fatalf("code=%s want=%s", reply.Code, tc.code)
			}
			b, _ := json.Marshal(reply)
			if strings.Contains(string(b), "synthetic-scope-token") {
				t.Fatal("token leaked")
			}
		})
	}
	// Neither verb is defined by canonical 01/03. Do not invent reachable RPCs.
	for _, verb := range []string{"integration.test", "integration.delete", "dns.record.create"} {
		reply := h.s.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: json.RawMessage(`{}`)})
		if reply.OK || reply.Code != "unknown_verb" {
			t.Fatalf("unapproved consumer %s became reachable", verb)
		}
	}
	h.forbidden = []string{"synthetic-scope-token"}
	h.records()
}

type integrationProbeFunc func(context.Context, *dns.IntegrationToken, []string) (dns.IntegrationSpec, error)

func (f integrationProbeFunc) Probe(c context.Context, t *dns.IntegrationToken, z []string) (dns.IntegrationSpec, error) {
	return f(c, t, z)
}

func TestIntegrationSetDispatcherSealedProbe(t *testing.T) {
	for _, mode := range []string{"success", "insufficient", "wrong zone", "extra zone", "expired", "publication failure"} {
		t.Run(mode, func(t *testing.T) {
			h := newPhase2AuditHarness(t)
			st := h.s.Agent.Store
			ctx := context.Background()
			// Test-only target DDL. Open still applies only the approved numbered migrations.
			raw, e := os.ReadFile("../../docs/spec/02-data-model.md")
			if e != nil {
				t.Fatal(e)
			}
			part := strings.SplitN(string(raw), "## Phase 7 — integrations (DNS)", 2)[1]
			ddl := strings.SplitN(strings.SplitN(part, "```sql\n", 2)[1], "```", 2)[0]
			if e = st.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec(ddl); return e }); e != nil {
				t.Fatal(e)
			}
			if mode == "publication failure" {
				if e = st.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.Exec(`CREATE TRIGGER reject_zone BEFORE INSERT ON dns_zones BEGIN SELECT RAISE(ABORT,'synthetic-scope-token'); END`)
					return e
				}); e != nil {
					t.Fatal(e)
				}
			}
			calls := 0
			var held *dns.IntegrationToken
			h.s.Agent.IntegrationProbe = integrationProbeFunc(func(c context.Context, token *dns.IntegrationToken, zones []string) (dns.IntegrationSpec, error) {
				calls++
				held = token
				b := token.ExecutionBytes()
				defer clear(b)
				if string(b) != "synthetic-scope-token" || len(zones) != 1 || zones[0] != "example.com" {
					t.Fatal("probe input")
				}
				spec := dns.IntegrationSpec{Kind: "cloudflare", Zones: []dns.ZoneBinding{{Zone: "example.com", ProviderZoneID: strings.Repeat("a", 32)}}}
				switch mode {
				case "insufficient":
					return spec, errors.New("synthetic-scope-token")
				case "wrong zone":
					spec.Zones[0].Zone = "other.com"
				case "extra zone":
					spec.Zones = append(spec.Zones, dns.ZoneBinding{Zone: "other.com", ProviderZoneID: strings.Repeat("b", 32)})
				case "expired":
					return spec, context.DeadlineExceeded
				}
				return spec, nil
			})
			c := rpc.RootCaller(0)
			c.MFAat = h.clock.now
			reply := h.s.Dispatch(rpc.WithCaller(ctx, c), rpc.Request{Verb: "integration.set", Args: json.RawMessage(`{"kind":"cloudflare","token":"synthetic-scope-token","zones":["example.com"]}`)})
			want := "forbidden"
			if mode == "success" {
				want = ""
			}
			if mode == "expired" {
				want = "deadline_exceeded"
			}
			if reply.Code != want || reply.OK != (want == "") {
				t.Fatalf("code %s want %s", reply.Code, want)
			}
			if calls != 1 || len(held.ExecutionBytes()) != 0 {
				t.Fatal("probe or token destruction")
			}
			if e = st.Tx(ctx, func(tx *sql.Tx) error {
				var n int
				if e := tx.QueryRow("SELECT count(*) FROM integrations").Scan(&n); e != nil {
					return e
				}
				want := 0
				if mode == "success" {
					want = 1
				}
				if n != want {
					t.Fatalf("partial publication %d", n)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if mode == "success" {
				b, _ := json.Marshal(reply.Data)
				var binding store.DNSIntegration
				if json.Unmarshal(b, &binding) != nil || binding.ID == "" {
					t.Fatal("canonical reply")
				}
				sealed, e := st.DNSIntegrationToken(ctx, store.IntegrationAuthority{LocalRoot: true}, "example.com", binding.ID)
				if e != nil {
					t.Fatal(e)
				}
				plain, e := h.s.Agent.Secret.Open(secret.Sealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
				if e != nil || string(plain) != "synthetic-scope-token" {
					t.Fatal("sealed credential")
				}
				clear(plain)
				if e = st.DeleteDNSIntegration(ctx, store.IntegrationAuthority{LocalRoot: true}, binding.ID); e != nil {
					t.Fatal(e)
				}
				if _, e = st.DNSIntegrationToken(ctx, store.IntegrationAuthority{LocalRoot: true}, "example.com", binding.ID); !errors.Is(e, store.ErrDNSIntegration) {
					t.Fatal("removed lifetime accepted")
				}
			}
			h.forbidden = []string{"synthetic-scope-token"}
			h.records()
		})
	}
}

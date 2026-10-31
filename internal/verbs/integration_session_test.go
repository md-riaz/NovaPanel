package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"novapanel/internal/dns"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestIntegrationSetSessionPublicationLifetime(t *testing.T) {
	for _, mode := range []string{"success", "demoted during probe", "publication rollback"} {
		t.Run(mode, func(t *testing.T) {
			h := newPhase2AuditHarness(t)
			ctx := context.Background()
			st := h.s.Agent.Store
			raw, err := os.ReadFile("../../docs/spec/02-data-model.md")
			if err != nil {
				t.Fatal(err)
			}
			part := strings.SplitN(string(raw), "## Phase 7 — integrations (DNS)", 2)[1]
			ddl := strings.SplitN(strings.SplitN(part, "```sql\n", 2)[1], "```", 2)[0]
			if err = st.Tx(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(ddl); return err }); err != nil {
				t.Fatal(err)
			}
			h.call("create-admin", "user.create", "", true, map[string]any{"username": "lifetimeadmin", "email": "lifetime@example.test", "role": "admin", "password": "synthetic-login-password"}, "", "owner", "exec")
			session := h.login("login-admin", "lifetimeadmin", "synthetic-login-password")
			caller, err := h.s.Sessions.Resolve(ctx, session)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.Tx(ctx, func(tx *sql.Tx) error {
				_, err := tx.Exec("UPDATE sessions SET mfa_at=? WHERE id=?", h.clock.now, caller.Session)
				if err != nil {
					return err
				}
				if mode == "publication rollback" {
					_, err = tx.Exec(`CREATE TRIGGER reject_session_zone BEFORE INSERT ON dns_zones BEGIN SELECT RAISE(ABORT,'synthetic-session-scope-token'); END`)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			var held *dns.IntegrationToken
			h.s.Agent.IntegrationProbe = integrationProbeFunc(func(_ context.Context, token *dns.IntegrationToken, _ []string) (dns.IntegrationSpec, error) {
				calls++
				held = token
				if mode == "demoted during probe" {
					if err := st.Tx(ctx, func(tx *sql.Tx) error {
						_, err := tx.Exec("UPDATE users SET role='read_only' WHERE id=?", caller.UserID)
						return err
					}); err != nil {
						t.Fatal(err)
					}
				}
				return dns.IntegrationSpec{Kind: "cloudflare", Zones: []dns.ZoneBinding{{Zone: "example.com", ProviderZoneID: strings.Repeat("a", 32)}}}, nil
			})
			want := "forbidden"
			if mode == "success" {
				want = ""
			}
			reply := h.call("publish-session-integration", "integration.set", session, false, map[string]any{"kind": "cloudflare", "token": "synthetic-session-scope-token", "zones": []string{"example.com"}}, want, "admin", "exec")
			if calls != 1 || held == nil || len(held.ExecutionBytes()) != 0 {
				t.Fatal("probe token lifetime")
			}
			if err := st.Tx(ctx, func(tx *sql.Tx) error {
				for _, query := range []string{"SELECT count(*) FROM integrations", "SELECT count(*) FROM dns_zones", "SELECT count(*) FROM integration_owners", "SELECT count(*) FROM secrets WHERE name LIKE 'integration:%'"} {
					var n int
					if err := tx.QueryRow(query).Scan(&n); err != nil {
						return err
					}
					expected := 0
					if mode == "success" {
						expected = 1
					}
					if n != expected {
						t.Fatalf("partial publication: %s = %d want %d", query, n, expected)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if mode == "success" {
				var binding store.DNSIntegration
				data, err := json.Marshal(reply.Data)
				if err != nil || json.Unmarshal(data, &binding) != nil || binding.ID == "" {
					t.Fatal("missing lifetime")
				}
				authority := store.IntegrationAuthority{UserID: caller.UserID}
				sealed, err := st.DNSIntegrationToken(ctx, authority, "example.com", binding.ID)
				if err != nil {
					t.Fatal(err)
				}
				plain, err := h.s.Agent.Secret.Open(secret.Sealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
				if err != nil || string(plain) != "synthetic-session-scope-token" {
					t.Fatal("sealed session publication")
				}
				clear(plain)
				if err := st.DeleteDNSIntegration(ctx, authority, binding.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := st.DNSIntegrationToken(ctx, authority, "example.com", binding.ID); !errors.Is(err, store.ErrDNSIntegration) {
					t.Fatal("removed lifetime accepted", err)
				}
			}
			h.forbidden = append(h.forbidden, "synthetic-session-scope-token")
			h.records()
		})
	}
}

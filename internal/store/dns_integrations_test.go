package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"novapanel/internal/dns"
	"novapanel/internal/secret"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The ownership companion is a proposed Phase 7 addition, not shipped DDL.
func dnsFixture(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	raw, e := os.ReadFile("../../docs/spec/02-data-model.md")
	if e != nil {
		t.Fatal(e)
	}
	part := strings.SplitN(string(raw), "## Phase 7 — integrations (DNS)", 2)[1]
	ddl := strings.SplitN(strings.SplitN(part, "```sql\n", 2)[1], "```", 2)[0]
	_, e = s.db.Exec(ddl + `CREATE TABLE integration_owners (
 integration_id TEXT PRIMARY KEY REFERENCES integrations(id) ON DELETE CASCADE,
 user_id TEXT REFERENCES users(id) ON DELETE RESTRICT,
 local_root INTEGER NOT NULL CHECK(local_root IN (0,1)),
 CHECK ((user_id IS NULL AND local_root=1) OR (user_id IS NOT NULL AND local_root=0)));
 CREATE UNIQUE INDEX dns_zone_name_unique ON dns_zones(zone);`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CreateUser(context.Background(), User{ID: "owner", Username: "owner", PWPHC: "fixture", Role: "admin"}); e != nil {
		t.Fatal(e)
	}
	return s, path
}
func dnsSealed(t *testing.T) IntegrationSealed {
	t.Helper()
	sealer, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	v, e := sealer.Seal([]byte("synthetic-integration-token"))
	if e != nil {
		t.Fatal(e)
	}
	return IntegrationSealed{Nonce: v.Nonce, Ciphertext: v.Ciphertext}
}
func dnsSpec() dns.IntegrationSpec {
	return dns.IntegrationSpec{Kind: "cloudflare", Zones: []dns.ZoneBinding{{Zone: "example.com", ProviderZoneID: strings.Repeat("a", 32)}}}
}
func TestDNSIntegrationDurableSealed(t *testing.T) {
	s, path := dnsFixture(t)
	ctx := context.Background()
	owner := IntegrationAuthority{UserID: "owner"}
	sealed := dnsSealed(t)
	binding, e := s.CreateDNSIntegration(ctx, owner, dnsSpec(), sealed)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.DNSIntegrationToken(ctx, owner, "example.com", binding.ID)
	if e != nil {
		t.Fatal(e)
	}
	sealer, _ := secret.New(make([]byte, 32))
	plain, e := sealer.Open(secret.Sealed{Nonce: got.Nonce, Ciphertext: got.Ciphertext})
	if e != nil || string(plain) != "synthetic-integration-token" {
		t.Fatal("sealed roundtrip", e)
	}
	raw, _ := json.Marshal(got)
	for _, out := range []string{string(raw), fmt.Sprintf("%v %+v %#v", got, got, got)} {
		if strings.Contains(out, "synthetic-integration-token") || strings.Contains(out, fmt.Sprint(got.Ciphertext)) {
			t.Fatal("sealed value exposed")
		}
	}
	if e = s.DeleteDNSIntegration(ctx, owner, binding.ID); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.db.QueryRow("SELECT count(*) FROM secrets WHERE name=?", "integration:"+binding.ID+":token").Scan(&n); e != nil || n != 0 {
		t.Fatal("orphan secret", n, e)
	}
	next, e := s.CreateDNSIntegration(ctx, owner, dnsSpec(), sealed)
	if e != nil {
		t.Fatal(e)
	}
	if next.ID == binding.ID {
		t.Fatal("reused lifetime")
	}
	if _, e = s.DNSIntegrationToken(ctx, owner, "example.com", binding.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("stale job binding accepted", e)
	}
}

func TestDNSIntegrationAuthorityRollback(t *testing.T) {
	s, _ := dnsFixture(t)
	ctx := context.Background()
	owner := IntegrationAuthority{UserID: "owner"}
	for _, a := range []IntegrationAuthority{{}, {UserID: "missing"}, {LocalRoot: true, UserID: "owner"}} {
		if _, e := s.CreateDNSIntegration(ctx, a, dnsSpec(), dnsSealed(t)); !errors.Is(e, ErrDNSIntegration) {
			t.Fatal(e)
		}
	}
	if _, e := s.db.Exec(`CREATE TRIGGER fail_dns_zone BEFORE INSERT ON dns_zones BEGIN SELECT RAISE(ABORT,'synthetic-integration-token'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateDNSIntegration(ctx, owner, dnsSpec(), dnsSealed(t)); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("unsanitized failure", e)
	}
	var n int
	if e := s.db.QueryRow("SELECT (SELECT count(*) FROM integrations)+(SELECT count(*) FROM secrets WHERE name LIKE 'integration:%')+(SELECT count(*) FROM integration_owners)").Scan(&n); e != nil || n != 0 {
		t.Fatal("partial publication", n, e)
	}
	if _, e := s.db.Exec("DROP TRIGGER fail_dns_zone"); e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateDNSIntegration(ctx, owner, dnsSpec(), dnsSealed(t))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec("UPDATE users SET role='read_only' WHERE id='owner'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DNSIntegrationToken(ctx, owner, "example.com", v.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("demoted authority accepted", e)
	}
	if _, e = s.db.Exec("DELETE FROM users WHERE id='owner'"); e == nil {
		t.Fatal("missing owner FK")
	}
	if _, e = s.db.Exec("UPDATE users SET role='admin',disabled=1 WHERE id='owner'"); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteDNSIntegration(ctx, owner, v.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("disabled owner accepted", e)
	}
	if e = s.DeleteDNSIntegration(ctx, IntegrationAuthority{LocalRoot: true}, v.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateDNSIntegration(ctx, IntegrationAuthority{}, dns.IntegrationSpec{}, IntegrationSealed{}); !errors.Is(e, ErrStoreClosed) {
		t.Fatal("closed precedence", e)
	}
	if _, e = s.DNSIntegrationToken(ctx, IntegrationAuthority{}, "", ""); !errors.Is(e, ErrStoreClosed) {
		t.Fatal(e)
	}
	if e = s.DeleteDNSIntegration(ctx, IntegrationAuthority{}, ""); !errors.Is(e, ErrStoreClosed) {
		t.Fatal(e)
	}
}

func TestDNSIntegrationMembershipAndCleanupFailures(t *testing.T) {
	s, _ := dnsFixture(t)
	ctx := context.Background()
	a := IntegrationAuthority{UserID: "owner"}
	if _, e := s.CreateDNSIntegration(ctx, a, dnsSpec(), IntegrationSealed{}); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal(e)
	}
	if _, e := s.CreateDNSIntegration(ctx, a, dns.IntegrationSpec{}, dnsSealed(t)); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal(e)
	}
	if e := s.CreateUser(ctx, User{ID: "other", Username: "other", PWPHC: "fixture", Role: "admin"}); e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateDNSIntegration(ctx, a, dnsSpec(), dnsSealed(t))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DNSIntegrationToken(ctx, IntegrationAuthority{UserID: "other"}, "example.com", v.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("wrong owner", e)
	}
	if _, e = s.DNSIntegrationToken(ctx, a, "other.com", v.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("wrong membership", e)
	}
	if _, e = s.DNSIntegrationToken(ctx, a, "EXAMPLE.com", v.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("normalized source token", e)
	}
	if _, e = s.db.Exec(`CREATE TRIGGER fail_integration_delete BEFORE DELETE ON integrations BEGIN SELECT RAISE(ABORT,'synthetic-integration-token'); END`); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteDNSIntegration(ctx, a, v.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal(e)
	}
	if _, e = s.DNSIntegrationToken(ctx, a, "example.com", v.ID); e != nil {
		t.Fatal("delete failure lost credential", e)
	}
	if _, e = s.db.Exec("DROP TRIGGER fail_integration_delete; UPDATE dns_zones SET provider_zone_id='../bad'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DNSIntegrationToken(ctx, a, "example.com", v.ID); !errors.Is(e, ErrDNSIntegration) {
		t.Fatal("corrupt binding accepted", e)
	}
}

func TestDNSIntegrationOwnerRole(t *testing.T) {
	s, _ := dnsFixture(t)
	if _, e := s.db.Exec("UPDATE users SET role='owner' WHERE id='owner'"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateDNSIntegration(context.Background(), IntegrationAuthority{UserID: "owner"}, dnsSpec(), dnsSealed(t)); e != nil {
		t.Fatal("owner role must satisfy admin minimum", e)
	}
}

func TestDNSIntegrationExpiryAndConcurrentMembership(t *testing.T) {
	s, _ := dnsFixture(t)
	ctx := context.Background()
	a := IntegrationAuthority{UserID: "owner"}
	sealed := dnsSealed(t)
	expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancel()
	if _, e := s.CreateDNSIntegration(expired, a, dnsSpec(), sealed); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.CreateDNSIntegration(ctx, a, dnsSpec(), sealed); results <- e }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrDNSIntegration) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("duplicate zone publications", success)
	}
}

func TestDNSIntegrationCloseDrainsBlockedPublication(t *testing.T) {
	s, path := dnsFixture(t)
	ctx := context.Background()
	a := IntegrationAuthority{UserID: "owner"}
	sealed := dnsSealed(t)
	conn, e := s.db.raw.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	before := s.db.raw.Stats().WaitCount
	result := make(chan error, 1)
	go func() { _, e := s.CreateDNSIntegration(ctx, a, dnsSpec(), sealed); result <- e }()
	deadline := time.After(3 * time.Second)
	for s.db.raw.Stats().WaitCount == before {
		select {
		case <-deadline:
			t.Fatal("publication did not wait")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case e := <-result:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("publication failed to cancel")
	}
	conn.Close()
	select {
	case e := <-closed:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not drain")
	}
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	var n int
	if e = reopened.db.QueryRow("SELECT count(*) FROM integrations").Scan(&n); e != nil || n != 0 {
		t.Fatal("canceled write survived", n, e)
	}
}

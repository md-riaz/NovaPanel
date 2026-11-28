package store

import (
	"context"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/secret"
	"testing"
)

func TestSetCloudflareIntegrationRotatesAndProtectsZones(t *testing.T) {
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	seal := func(value string) ACMESealed {
		v, err := sealer.Seal([]byte(value))
		if err != nil {
			t.Fatal(err)
		}
		return ACMESealed{Nonce: v.Nonce, Ciphertext: v.Ciphertext}
	}
	first, _ := id.New()
	owner, err := s.SetCloudflareIntegration(ctx, first, seal("first-token"), map[string]string{"example.com": "old"})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := id.New()
	again, err := s.SetCloudflareIntegration(ctx, next, seal("second-token"), map[string]string{"example.com": "new", "other.example": "other-id"})
	if err != nil || again != owner {
		t.Fatalf("re-set integration: %v", err)
	}
	zone, err := s.DNSZoneByName(ctx, "example.com")
	if err != nil || zone.IntegrationID != owner || zone.ProviderZoneID != "new" {
		t.Fatal("zone mapping lost")
	}
	var count int
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM integrations").Scan(&count); err != nil || count != 1 {
		t.Fatal("integration duplicated")
	}
	saved, err := s.DNSCredential(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := sealer.Open(secret.Sealed{Nonce: saved.Nonce, Ciphertext: saved.Ciphertext})
	if err != nil || string(plain) != "second-token" {
		t.Fatal("token not rotated")
	}
	duplicate, _ := id.New()
	if _, err = s.db.ExecContext(ctx, "INSERT INTO dns_zones(id,integration_id,zone,provider_zone_id,created) VALUES(?,?,?,?,?)", duplicate, owner, "example.com", "duplicate", 1); err == nil {
		t.Fatal("duplicate zone accepted")
	}
	otherOwner, _ := id.New()
	if _, err = s.SetCloudflareIntegration(ctx, otherOwner, seal("other-token"), map[string]string{"separate.example": "separate"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetCloudflareIntegration(ctx, next, seal("third-token"), map[string]string{"example.com": "changed", "separate.example": "changed"}); !errors.Is(err, ErrZoneManaged) {
		t.Fatalf("two owners accepted: %v", err)
	}
	foreign, _ := id.New()
	foreignZone, _ := id.New()
	if _, err = s.db.ExecContext(ctx, "INSERT INTO integrations(id,kind,name,config_json,created) VALUES(?,'foreign','foreign','{}',1)", foreign); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, "INSERT INTO dns_zones(id,integration_id,zone,provider_zone_id,created) VALUES(?,?,?,?,1)", foreignZone, foreign, "foreign.example", "foreign-id"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetCloudflareIntegration(ctx, next, seal("third-token"), map[string]string{"example.com": "changed", "foreign.example": "changed"}); !errors.Is(err, ErrZoneManaged) {
		t.Fatalf("foreign zone: %v", err)
	}
	saved, err = s.DNSCredential(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	plain, err = sealer.Open(secret.Sealed{Nonce: saved.Nonce, Ciphertext: saved.Ciphertext})
	if err != nil || string(plain) != "second-token" {
		t.Fatal("conflict rotated token")
	}
	zone, err = s.DNSZoneByName(ctx, "example.com")
	if err != nil || zone.ProviderZoneID != "new" {
		t.Fatal("conflict changed mapping")
	}
}

func TestCreateIntegrationSealedAtomically(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal([]byte("example-token"))
	if err != nil {
		t.Fatal(err)
	}
	integrationID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	v := ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}
	if err = s.CreateIntegration(context.Background(), integrationID, `{"kind":"cloudflare","zones":["example.com"]}`, v); err != nil {
		t.Fatal(err)
	}
	var nonce, ciphertext []byte
	if err = s.db.QueryRowContext(context.Background(), "SELECT nonce,ciphertext FROM secrets WHERE name=?", "integration:"+integrationID+":token").Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	plain, err := sealer.Open(secret.Sealed{Nonce: nonce, Ciphertext: ciphertext})
	if err != nil || string(plain) != "example-token" {
		t.Fatal("sealed secret failed round trip")
	}
	if err = s.CreateIntegration(context.Background(), integrationID, `{}`, v); err == nil {
		t.Fatal("duplicate accepted")
	}
}

package store

import (
	"context"
	"database/sql"
	"testing"

	"novapanel/internal/dns"
	"novapanel/internal/id"
	"novapanel/internal/secret"
)

func TestDNSRecordReplay(t *testing.T) {
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
	sealed, err := sealer.Seal([]byte("example"))
	if err != nil {
		t.Fatal(err)
	}
	integrationID, _ := id.New()
	if err := s.CreateIntegration(ctx, integrationID, "{}", ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, map[string]string{"example.com": "zone-provider"}); err != nil {
		t.Fatal(err)
	}
	var zoneID string
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM dns_zones WHERE integration_id=?", integrationID).Scan(&zoneID); err != nil {
		t.Fatal(err)
	}
	objID, _ := id.New()
	r := DNSRecord{ObjID: objID, ZoneID: zoneID, Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1}
	if err := s.ReserveDNSRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, err = s.DNSRecord(ctx, objID)
	if err != nil {
		t.Fatal(err)
	}
	p := &dns.Fake{}
	foreign := dns.Record{ID: "foreign", Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL}
	if _, err := p.Create(ctx, "example.com", foreign); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDNSCreate(ctx, p, "example.com", r); err != ErrDNSOwnership {
		t.Fatalf("foreign record accepted: %v", err)
	}
	if err := p.Delete(ctx, "example.com", foreign); err != nil {
		t.Fatal(err)
	}
	providerID, err := p.Create(ctx, "example.com", dns.Record{Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Comment: dnsComment(objID)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileDNSCreate(ctx, p, "example.com", r); err != nil {
		t.Fatal(err)
	}
	r, err = s.DNSRecord(ctx, objID)
	if err != nil || r.ProviderRecordID.String != providerID || r.Phase != "created" {
		t.Fatalf("created: %+v %v", r, err)
	}
	if err := s.ReconcileDNSCreate(ctx, p, "example.com", r); err != nil {
		t.Fatal(err)
	}
	records, _ := p.List(ctx, "example.com")
	if len(records) != 1 {
		t.Fatalf("duplicate records: %d", len(records))
	}
	r.Value = "192.0.2.2"
	if err := p.Update(ctx, "example.com", dns.Record{ID: providerID, Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Comment: dnsComment(objID)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateManagedDNSRecord(ctx, p, "example.com", r); err != nil {
		t.Fatal(err)
	}
	saved, err := s.DNSRecord(ctx, objID)
	if err != nil || saved.Value != r.Value {
		t.Fatalf("update: %+v %v", saved, err)
	}
	if err := s.MarkDNSRecordDeleting(ctx, objID); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, "example.com", dns.Record{ID: providerID}); err != nil {
		t.Fatal(err)
	}
	saved, err = s.DNSRecord(ctx, objID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteManagedDNSRecord(ctx, p, "example.com", saved); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DNSRecord(ctx, objID); err != sql.ErrNoRows {
		t.Fatalf("record remains: %v", err)
	}
}

package verbs

import (
	"context"
	"encoding/json"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/dns"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestDNSValidation(t *testing.T) {
	tests := []struct {
		r  store.DNSRecord
		ok bool
	}{
		{store.DNSRecord{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1, Proxied: true}, true},
		{store.DNSRecord{Type: "A", Name: "www.example.com", Value: "::1", TTL: 1}, false},
		{store.DNSRecord{Type: "TXT", Name: "_dmarc.example.com", Value: "v=DMARC1", TTL: 1}, true},
		{store.DNSRecord{Type: "TXT", Name: "_dmarc.example.com", Value: "v=DMARC1", TTL: 1, Proxied: true}, false},
		{store.DNSRecord{Type: "MX", Name: "example.com", Value: "mail.example.com", TTL: 1, Priority: 10, Proxied: true}, false},
		{store.DNSRecord{Type: "CNAME", Name: "evil.example.net", Value: "example.com", TTL: 1}, false},
	}
	for _, tt := range tests {
		if got := validateDNSRecord("example.com", tt.r, false); (got == nil) != tt.ok {
			t.Errorf("%+v: %v", tt.r, got)
		}
	}
}
func TestDNSCRUDAndSweep(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal([]byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	integrationID, _ := id.New()
	if err = s.CreateIntegration(ctx, integrationID, "{}", store.ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, map[string]string{"example.com": "zone"}); err != nil {
		t.Fatal(err)
	}
	fake := &dns.Fake{}
	a := &agent.Agent{Store: s, Secret: sealer, DNSProvider: func(token string, zones map[string]string) dns.Provider {
		if token != "test-token" || zones["example.com"] != "zone" {
			t.Fatal("incorrect provider binding")
		}
		return fake
	}}
	recordID, _ := id.New()
	create := dnsCreateArgs{Zone: "example.com", Type: "A", Name: "www.example.com", Value: "192.0.2.1", RecordID: recordID}
	raw, _ := json.Marshal(create)
	j := &jobs.Job{Verb: "dns.record.create", ObjectKey: "zone:example.com", Args: raw}
	if err = (dnsState{a}).execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	result, err := dnsList(ctx, a, &dnsZoneArgs{Zone: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	if string(b) == "" {
		t.Fatal("empty result")
	}
	value := "192.0.2.2"
	update := dnsUpdateArgs{Zone: "example.com", RecordID: recordID, Value: &value}
	raw, _ = json.Marshal(update)
	j = &jobs.Job{Verb: "dns.record.update", ObjectKey: "zone:example.com", Args: raw}
	if err = (dnsState{a}).execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	records, _ := fake.List(ctx, "example.com")
	if len(records) != 1 || records[0].Value != value {
		t.Fatal("update missing")
	}
	record, err := s.DNSRecord(ctx, recordID)
	if err != nil {
		t.Fatal(err)
	}
	record.Value = "192.0.2.3"
	if err = s.BeginDNSUpdate(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err = reconcileDNS(ctx, a); err != nil {
		t.Fatal(err)
	}
	records, _ = fake.List(ctx, "example.com")
	if len(records) != 1 || records[0].Value != "192.0.2.3" {
		t.Fatal("sweep did not apply intent")
	}
	raw, _ = json.Marshal(dnsIDArgs{Zone: "example.com", RecordID: recordID})
	j = &jobs.Job{Verb: "dns.record.delete", ObjectKey: "zone:example.com", Args: raw}
	if err = (dnsState{a}).execute(ctx, j); err != nil {
		t.Fatal(err)
	}
	records, _ = fake.List(ctx, "example.com")
	if len(records) != 0 {
		t.Fatal("delete missing")
	}
}

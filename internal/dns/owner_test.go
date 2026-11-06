package dns_test

import (
	"novapanel/internal/dns"
	"testing"
)

func TestServiceOwnerRecord(t *testing.T) {
	for _, name := range []string{"_acme-challenge.example.com", "_dmarc.example.com", "selector._domainkey.example.com"} {
		if err := (dns.Record{Type: "TXT", Name: name, Value: "fixture", TTL: 1}).Validate("example.com"); err != nil {
			t.Fatal(err)
		}
	}
}

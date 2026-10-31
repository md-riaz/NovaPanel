package dns_test

import (
	"errors"
	"novapanel/internal/dns"
	"novapanel/internal/rpcerr"
	"testing"
)

func TestRecordValidation(t *testing.T) {
	valid := []dns.Record{
		{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1, Proxied: true},
		{Type: "AAAA", Name: "www.example.com", Value: "2001:db8::1", TTL: 300},
		{Type: "CNAME", Name: "www.example.com", Value: "origin.example.net", TTL: 300},
		{Type: "TXT", Name: "example.com", Value: "v=spf1 -all", TTL: 300},
		{Type: "MX", Name: "example.com", Value: "mail.example.com", TTL: 300, Priority: 10},
	}
	for _, r := range valid {
		if err := r.Validate("example.com"); err != nil {
			t.Fatalf("valid %s: %v", r.Type, err)
		}
	}
	bad := []dns.Record{
		{Type: "A", Name: "www.example.com", Value: "::1", TTL: 1},
		{Type: "AAAA", Name: "www.example.com", Value: "192.0.2.1", TTL: 1},
		{Type: "AAAA", Name: "www.example.com", Value: "fe80::1%eth0", TTL: 1},
		{Type: "CNAME", Name: "www.example.com", Value: "target;evil", TTL: 1},
		{Type: "A", Name: "example.com.evil.net", Value: "192.0.2.1", TTL: 1},
		{Type: "A", Name: "badexample.com", Value: "192.0.2.1", TTL: 1},
		{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 0},
		{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 300, Priority: 10},
		{Type: "MX", Name: "example.com", Value: "mail.example.com", TTL: 300, Proxied: true},
		{Type: "TXT", Name: "example.com", Value: "secret\x00bad", TTL: 300},
		{Type: "TXT", Name: "example.com", Value: "v=spf1 -all", TTL: 300, Proxied: true},
		{Type: "NS", Name: "example.com", Value: "ns.example.com", TTL: 300},
	}
	for i, r := range bad {
		var coded *rpcerr.CodedError
		err := r.Validate("example.com")
		if !errors.As(err, &coded) || coded.Code != "invalid_argument" {
			t.Fatalf("bad case %d: %v", i, err)
		}
	}
}

package tls

import (
	"bytes"
	"crypto/x509"
	"strings"
	"testing"
	"time"
)

func TestCertificateRefusals(t *testing.T) {
	cert, key := fixture(t, nil)
	_, otherKey := fixture(t, nil)
	for _, tc := range []struct {
		name      string
		cert, key []byte
		domains   []string
	}{
		{"mismatch", cert, otherKey, []string{"example.test"}},
		{"missing SAN", cert, key, []string{"other.test"}},
		{"host injection", cert, key, []string{"example.test;"}},
		{"no domains", cert, key, nil},
		{"oversized cert", bytes.Repeat([]byte("a"), MaxCertificateBytes+1), key, []string{"example.test"}},
		{"oversized key", cert, bytes.Repeat([]byte("a"), MaxKeyBytes+1), []string{"example.test"}},
		{"junk prefix", append([]byte("junk"), cert...), key, []string{"example.test"}},
		{"junk suffix", append(append([]byte(nil), cert...), []byte("junk")...), key, []string{"example.test"}},
		{"key trailing block", cert, append(append([]byte(nil), key...), key...), []string{"example.test"}},
		{"empty cert", nil, key, []string{"example.test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Validate(tc.cert, tc.key, tc.domains, testNow); err == nil {
				t.Fatal("accepted invalid certificate")
			} else if strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Fatal("secret error")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*x509.Certificate)
	}{
		{"expired", func(c *x509.Certificate) { c.NotAfter = testNow }},
		{"future", func(c *x509.Certificate) { c.NotBefore = testNow.Add(time.Second) }},
		{"CN only", func(c *x509.Certificate) { c.DNSNames = nil }},
		{"client only", func(c *x509.Certificate) { c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} }},
		{"CA", func(c *x509.Certificate) { c.IsCA = true; c.BasicConstraintsValid = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, k := fixture(t, tc.change)
			if _, err := Validate(c, k, []string{"example.test"}, testNow); err == nil {
				t.Fatal("accepted invalid leaf")
			}
		})
	}
}

func TestWildcardSAN(t *testing.T) {
	cert, key := fixture(t, func(c *x509.Certificate) { c.DNSNames = []string{"*.example.test"} })
	c, err := Validate(cert, key, []string{"www.example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if c.Covers("example.test") || c.Covers("deep.www.example.test") {
		t.Fatal("wildcard coverage too broad")
	}
}

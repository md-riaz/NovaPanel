package validate_test

import (
	"novapanel/internal/validate"
	"strings"
	"testing"
)

func TestDNSOwnerSeparateFromHostname(t *testing.T) {
	v := &validate.V{}
	for _, s := range []string{"_acme-challenge.example.com", "_dmarc.example.com", "selector._domainkey.example.com", "a_b.example.com"} {
		if err := v.DNSOwner(s); err != nil {
			t.Fatal(s, err)
		}
		if v.Hostname(s) == nil {
			t.Fatal("web hostname weakened")
		}
	}
	for _, s := range []string{"", "*.example.com", "a..example.com", ".example.com", "example.com.", "a b.com", "a\t.com", "a\n.com", "é.com", "a;.com", "-a.com", "a-.com", strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 127) + "aa"} {
		if v.DNSOwner(s) == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

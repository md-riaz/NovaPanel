// Package dns holds typed DNS records without credentials or persistent state.
package dns

import (
	"net/netip"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"strings"
	"unicode/utf8"
)

// Record is the Phase 7 provider record, not an RPC reply projection.
type Record struct {
	ID       string
	Type     string
	Name     string
	Value    string
	TTL      int
	Priority int
	Proxied  bool
}

// Validate checks an ordinary hostname record against its authorized zone.
// Underscore service/authentication owners require a canonical F9 amendment;
// callers must not bypass Hostname to enable them or ACME challenge records.
func (r Record) Validate(zone string) error {
	invalid := func() error { return rpcerr.Errf("invalid_argument", "Invalid DNS record") }
	v := &validate.V{}
	if v.Hostname(zone) != nil || v.Hostname(r.Name) != nil {
		return invalid()
	}
	name, z := strings.ToLower(r.Name), strings.ToLower(zone)
	if name != z && !strings.HasSuffix(name, "."+z) {
		return invalid()
	}
	// Cloudflare standard-zone TTL range; Enterprise-only 30-second TTL is not enabled.
	if r.TTL != 1 && (r.TTL < 60 || r.TTL > 86400) {
		return invalid()
	}
	if r.Priority < 0 || r.Priority > 65535 || (r.Type != "MX" && r.Priority != 0) {
		return invalid()
	}
	if r.Proxied && r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME" {
		return invalid()
	}
	switch r.Type {
	case "A", "AAAA":
		ip, err := netip.ParseAddr(r.Value)
		if err != nil || ip.Zone() != "" || (r.Type == "A" && !ip.Is4()) || (r.Type == "AAAA" && (!ip.Is6() || ip.Is4In6())) {
			return invalid()
		}
	case "CNAME", "MX":
		if v.Hostname(r.Value) != nil {
			return invalid()
		}
	case "TXT":
		if !utf8.ValidString(r.Value) || strings.ContainsAny(r.Value, "\x00\r\n") {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}

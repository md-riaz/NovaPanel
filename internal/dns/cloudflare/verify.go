package cloudflare

import (
	"context"
	"net/netip"
	"net/url"
	"novapanel/internal/dns"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"strings"
	"unicode"
)

func (c *Client) Verify(ctx context.Context, domain string, resolver dns.Resolver, expected []netip.Addr) (dns.Verification, error) {
	result := dns.Verification{Method: "resolver"}
	if (&validate.V{}).Hostname(domain) != nil || resolver == nil || len(expected) == 0 {
		return result, rpcerr.Errf("invalid_argument", "Invalid DNS verification input")
	}
	for _, ip := range expected {
		if !ip.IsValid() || ip.Zone() != "" {
			return result, rpcerr.Errf("invalid_argument", "Invalid DNS verification address")
		}
	}
	zone := ""
	domain = strings.ToLower(domain)
	for z := range c.zones {
		if (domain == z || strings.HasSuffix(domain, "."+z)) && len(z) > len(zone) {
			zone = z
		}
	}
	if zone == "" {
		return result, rpcerr.Errf("forbidden", "Cloudflare zone is not authorized")
	}
	records, err := c.List(ctx, zone)
	if err != nil {
		return result, err
	}
	for _, r := range records {
		if r.Name == domain && r.Proxied && (r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME") {
			return dns.Verification{PointsHere: true, Method: "provider"}, nil
		}
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", domain)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, rpcerr.Errf("agent_error", "DNS resolution failed")
	}
	// Mixed origins must not count as a verified route to this host.
	result.PointsHere = len(ips) > 0
	for _, ip := range ips {
		found := false
		for _, want := range expected {
			if ip.Unmap() == want.Unmap() {
				found = true
				break
			}
		}
		if !found {
			result.PointsHere = false
		}
	}
	return result, nil
}
func (c *Client) Purge(ctx context.Context, zone string, urls []string) error {
	path, err := c.zonePath(zone)
	if err != nil {
		return err
	}
	if len(urls) > 30 {
		return rpcerr.Errf("invalid_argument", "Too many purge URLs")
	}
	for _, s := range urls {
		u, e := url.Parse(s)
		if e != nil || len(s) > 2048 || strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Port() != "" || (&validate.V{}).Hostname(u.Hostname()) != nil {
			return rpcerr.Errf("invalid_argument", "Invalid purge URL")
		}
		host, z := strings.ToLower(u.Hostname()), strings.ToLower(zone)
		if host != z && !strings.HasSuffix(host, "."+z) {
			return rpcerr.Errf("invalid_argument", "Purge URL outside zone")
		}
	}
	var body any = struct {
		Everything bool `json:"purge_everything"`
	}{true}
	if len(urls) > 0 {
		body = struct {
			Files []string `json:"files"`
		}{urls}
	}
	var env struct {
		Success bool `json:"success"`
		Result  *struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err = c.request(ctx, "POST", path+"/purge_cache", body, &env); err != nil {
		return err
	}
	if !env.Success || env.Result == nil || env.Result.ID == "" {
		return providerError()
	}
	return nil
}

package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"novapanel/internal/dns"
	"novapanel/internal/validate"
	"strings"
	"time"
)

// ErrAttestationUnsupported means read access or token validity was not enough
// to establish the exact canonical permissions. It never contains upstream data.
var ErrAttestationUnsupported = errors.New("Cloudflare permission attestation unsupported")

type AttestationError struct{}

func (*AttestationError) Error() string { return ErrAttestationUnsupported.Error() }
func (*AttestationError) Unwrap() error { return ErrAttestationUnsupported }
func unattested() error                 { return &AttestationError{} }

// PermissionProbe is experimental and is deliberately not installed on Agent.
// Token Details requires API Tokens Read/Write beyond canonical zone permissions.
// Fake HTTP acceptance proves parsing, not availability for minimum-scope tokens.
type PermissionProbe struct{ transport http.RoundTripper }

var _ dns.IntegrationProbe = (*PermissionProbe)(nil)

func NewPermissionProbe(transport http.RoundTripper) *PermissionProbe {
	return &PermissionProbe{transport: transport}
}

type probeEnvelope[T any] struct {
	Success bool              `json:"success"`
	Errors  []json.RawMessage `json:"errors"`
	Result  T                 `json:"result"`
	Info    struct {
		Page  int `json:"page"`
		Count int `json:"count"`
		Total int `json:"total_count"`
		Pages int `json:"total_pages"`
	} `json:"result_info"`
}

func (e probeEnvelope[T]) valid() bool { return e.Success && len(e.Errors) == 0 }
func (e probeEnvelope[T]) single() bool {
	return e.valid() && e.Info.Page == 1 && e.Info.Count == 1 && e.Info.Total == 1 && e.Info.Pages <= 1
}

// Probe uses only GET requests and rejects every policy form it cannot prove.
// In particular, readable DNS records never imply DNS write or cache purge.
func (p *PermissionProbe) Probe(ctx context.Context, token *dns.IntegrationToken, zones []string) (dns.IntegrationSpec, error) {
	empty := dns.IntegrationSpec{}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if token == nil || len(zones) == 0 || len(zones) > 100 {
		return empty, dns.ErrIntegration
	}
	seen := map[string]bool{}
	for _, z := range zones {
		if z != strings.ToLower(z) || (&validate.V{}).Hostname(z) != nil || seen[z] {
			return empty, dns.ErrIntegration
		}
		seen[z] = true
	}
	plain := token.ExecutionBytes()
	defer clear(plain)
	checked, err := dns.NewIntegrationToken(plain)
	if err != nil {
		return empty, dns.ErrIntegration
	}
	checked.Destroy()
	c := &Client{token: func() string { return string(plain) }, http: &http.Client{Transport: p.transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	get := func(path string, out any) error {
		if err := c.request(ctx, http.MethodGet, path, nil, out); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return unattested()
		}
		return nil
	}
	var verify probeEnvelope[struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}]
	if err = get("/user/tokens/verify", &verify); err != nil {
		return empty, err
	}
	if !verify.valid() || !identifier.MatchString(verify.Result.ID) || verify.Result.Status != "active" {
		return empty, unattested()
	}
	spec := dns.IntegrationSpec{Kind: "cloudflare"}
	allowed := map[string]bool{}
	for _, z := range zones {
		var env probeEnvelope[[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}]
		if err = get("/zones?name="+url.QueryEscape(z)+"&page=1&per_page=2", &env); err != nil {
			return empty, err
		}
		if !env.single() || len(env.Result) != 1 || env.Result[0].Name != z || !identifier.MatchString(env.Result[0].ID) {
			return empty, unattested()
		}
		id := env.Result[0].ID
		if allowed[id] {
			return empty, unattested()
		}
		allowed[id] = true
		spec.Zones = append(spec.Zones, dns.ZoneBinding{Zone: z, ProviderZoneID: id})
	}
	// Resolve IDs through provider metadata, not names supplied inside a policy.
	required := map[string]bool{}
	for _, name := range []string{"Zone Read", "DNS Write", "Cache Purge"} {
		var env probeEnvelope[[]struct {
			ID     string   `json:"id"`
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}]
		if err = get("/user/tokens/permission_groups?name="+url.QueryEscape(name)+"&scope=com.cloudflare.api.account.zone", &env); err != nil {
			return empty, err
		}
		if !env.single() || len(env.Result) != 1 {
			return empty, unattested()
		}
		g := env.Result[0]
		if g.Name != name || !identifier.MatchString(g.ID) || required[g.ID] || len(g.Scopes) != 1 || g.Scopes[0] != "com.cloudflare.api.account.zone" {
			return empty, unattested()
		}
		required[g.ID] = true
	}
	var details probeEnvelope[struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Policies []struct {
			Effect string `json:"effect"`
			Groups []struct {
				ID string `json:"id"`
			} `json:"permission_groups"`
			Resources map[string]json.RawMessage `json:"resources"`
		} `json:"policies"`
	}]
	if err = get("/user/tokens/"+verify.Result.ID, &details); err != nil {
		return empty, err
	}
	if !details.valid() || details.Result.ID != verify.Result.ID || details.Result.Status != "active" || len(details.Result.Policies) == 0 {
		return empty, unattested()
	}
	grants := map[string]map[string]bool{}
	for _, policy := range details.Result.Policies {
		if policy.Effect != "allow" || len(policy.Groups) == 0 || len(policy.Resources) == 0 {
			return empty, unattested()
		}
		for _, g := range policy.Groups {
			if !required[g.ID] {
				return empty, unattested()
			}
		}
		for resource, value := range policy.Resources {
			const prefix = "com.cloudflare.api.account.zone."
			if !strings.HasPrefix(resource, prefix) {
				return empty, unattested()
			}
			id := strings.TrimPrefix(resource, prefix)
			var wildcard string
			if !allowed[id] || json.Unmarshal(value, &wildcard) != nil || wildcard != "*" {
				return empty, unattested()
			}
			if grants[id] == nil {
				grants[id] = map[string]bool{}
			}
			for _, g := range policy.Groups {
				grants[id][g.ID] = true
			}
		}
	}
	for id := range allowed {
		for permission := range required {
			if !grants[id][permission] {
				return empty, unattested()
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	return spec, nil
}

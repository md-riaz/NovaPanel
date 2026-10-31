package cloudflare_test

import (
	"context"
	"encoding/json"

	"io"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"strings"
	"testing"
)

const probeID = "023e105f4ecef8ad9ca31a8372d0c353"
const probeToken = "fixture-not-a-real-credential"

func probeReply(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}
func probeFixture(t *testing.T, change func(*http.Request, map[string]any)) http.RoundTripper {
	return transport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.Header.Get("Authorization") != "Bearer "+probeToken {
			t.Fatal("unsafe request")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		env := map[string]any{"success": true, "errors": []any{}}
		switch r.URL.Path {
		case "/client/v4/user/tokens/verify":
			env["result"] = map[string]any{"id": probeID, "status": "active"}
		case "/client/v4/zones":
			if r.URL.Query().Get("name") != "example.com" {
				t.Fatal("wrong zone filter")
			}
			env["result"] = []any{map[string]any{"id": probeID, "name": "example.com"}}
			env["result_info"] = map[string]any{"page": 1, "total_pages": 1, "count": 1, "total_count": 1}
		case "/client/v4/user/tokens/permission_groups":
			name := r.URL.Query().Get("name")
			id := map[string]string{"Zone Read": strings.Repeat("1", 32), "DNS Write": strings.Repeat("2", 32), "Cache Purge": strings.Repeat("3", 32)}[name]
			if id == "" {
				t.Fatal("unknown permission filter")
			}
			env["result"] = []any{map[string]any{"id": id, "name": name, "scopes": []string{"com.cloudflare.api.account.zone"}}}
			env["result_info"] = map[string]any{"page": 1, "count": 1, "total_count": 1}
		case "/client/v4/user/tokens/" + probeID:
			groups := []any{}
			for _, n := range []string{"1", "2", "3"} {
				groups = append(groups, map[string]any{"id": strings.Repeat(n, 32)})
			}
			env["result"] = map[string]any{"id": probeID, "status": "active", "policies": []any{map[string]any{"effect": "allow", "permission_groups": groups, "resources": map[string]any{"com.cloudflare.api.account.zone." + probeID: "*"}}}}
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
		if change != nil {
			change(r, env)
		}
		return probeReply(env), nil
	})
}
func TestPermissionProbeKnownPolicy(t *testing.T) {
	p := cloudflare.NewPermissionProbe(probeFixture(t, nil))
	token, _ := dns.NewIntegrationToken([]byte(probeToken))
	defer token.Destroy()
	spec, err := p.Probe(context.Background(), token, []string{"example.com"})
	if err != nil || spec.Validate() != nil || spec.Zones[0].ProviderZoneID != probeID {
		t.Fatalf("attestation failed: %v", err)
	}
}

package cloudflare_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"strings"
	"testing"
	"time"
)

func TestPermissionProbeUnknownFailsClosed(t *testing.T) {
	for _, kind := range []string{"verify-only", "missing-write", "unknown-id", "name-spoof", "deny", "wildcard", "extra-zone", "nested-account", "wrong-token", "wrong-zone", "pagination", "errors", "unknown-scope"} {
		t.Run(kind, func(t *testing.T) {
			p := cloudflare.NewPermissionProbe(probeFixture(t, func(r *http.Request, e map[string]any) {
				if kind == "verify-only" && r.URL.Path == "/client/v4/user/tokens/"+probeID {
					e["result"] = map[string]any{"id": probeID, "status": "active"}
					return
				}
				if r.URL.Path == "/client/v4/zones" {
					if kind == "wrong-zone" {
						e["result"].([]any)[0].(map[string]any)["name"] = "other.com"
					}
					if kind == "pagination" {
						e["result_info"].(map[string]any)["total_count"] = 2
					}
				}
				if kind == "unknown-scope" && r.URL.Path == "/client/v4/user/tokens/permission_groups" {
					e["result"].([]any)[0].(map[string]any)["scopes"] = []string{"com.cloudflare.api.account"}
				}
				if r.URL.Path != "/client/v4/user/tokens/"+probeID {
					return
				}
				result := e["result"].(map[string]any)
				policy := result["policies"].([]any)[0].(map[string]any)
				switch kind {
				case "missing-write":
					policy["permission_groups"] = []any{map[string]any{"id": strings.Repeat("1", 32)}, map[string]any{"id": strings.Repeat("3", 32)}}
				case "unknown-id", "name-spoof":
					policy["permission_groups"].([]any)[1] = map[string]any{"id": strings.Repeat("4", 32), "name": "DNS Write"}
				case "deny":
					policy["effect"] = "deny"
				case "wildcard":
					policy["resources"] = map[string]any{"com.cloudflare.api.account.zone.*": "*"}
				case "extra-zone":
					policy["resources"].(map[string]any)["com.cloudflare.api.account.zone."+strings.Repeat("4", 32)] = "*"
				case "nested-account":
					policy["resources"] = map[string]any{"com.cloudflare.api.account." + probeID: map[string]any{"com.cloudflare.api.account.zone.*": "*"}}
				case "wrong-token":
					result["id"] = strings.Repeat("4", 32)
				case "errors":
					e["errors"] = []any{map[string]any{"message": probeToken}}
				}
			}))
			token, _ := dns.NewIntegrationToken([]byte(probeToken))
			defer token.Destroy()
			spec, err := p.Probe(context.Background(), token, []string{"example.com"})
			var typed *cloudflare.AttestationError
			if !errors.As(err, &typed) || !errors.Is(err, cloudflare.ErrAttestationUnsupported) || len(spec.Zones) != 0 {
				t.Fatalf("unsafe acceptance: %v", err)
			}
			if strings.Contains(fmt.Sprintf("%+v %#v", err, p), probeToken) {
				t.Fatal("secret diagnostic")
			}
		})
	}
}
func TestPermissionProbeHTTPFailures(t *testing.T) {
	for _, kind := range []string{"redirect", "denied", "malformed", "oversized", "transport", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			p := cloudflare.NewPermissionProbe(transport(func(r *http.Request) (*http.Response, error) {
				calls++
				switch kind {
				case "transport":
					return nil, errors.New(probeToken)
				case "deadline":
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				status, body := 200, "{"
				switch kind {
				case "redirect":
					status = 302
				case "denied":
					status = 403
				case "oversized":
					body = strings.Repeat("x", (1<<20)+1)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://evil.example/" + probeToken}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			token, _ := dns.NewIntegrationToken([]byte(probeToken))
			defer token.Destroy()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			spec, err := p.Probe(ctx, token, []string{"example.com"})
			if err == nil || len(spec.Zones) != 0 || calls != 1 || strings.Contains(err.Error(), probeToken) {
				t.Fatal("unsafe HTTP failure")
			}
			if kind == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
		})
	}
}
func TestPermissionProbeInvalidInputNoHTTP(t *testing.T) {
	p := cloudflare.NewPermissionProbe(transport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil }))
	token, _ := dns.NewIntegrationToken([]byte(probeToken))
	defer token.Destroy()
	for _, zones := range [][]string{nil, {"Example.com"}, {"example.com", "example.com"}, {"https://evil.example"}, make([]string, 101)} {
		if _, err := p.Probe(context.Background(), token, zones); err == nil {
			t.Fatal("invalid zones accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Probe(ctx, token, []string{"example.com"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	token.Destroy()
	if _, err := p.Probe(context.Background(), token, []string{"example.com"}); err == nil {
		t.Fatal("destroyed token accepted")
	}
}

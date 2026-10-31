package cloudflare_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCreateFixedEndpointAndTypedBody(t *testing.T) {
	calls := 0
	c, err := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.String() != "https://api.cloudflare.com/client/v4/zones/023e105f4ecef8ad9ca31a8372d0c353/dns_records" {
			t.Fatal("unexpected request destination")
		}
		if r.Header.Get("Authorization") == "" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("missing headers")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["content"] != "192.0.2.1" || body["type"] != "A" || body["proxied"] != true {
			t.Fatal("incorrect typed record body")
		}
		if _, ok := body["value"]; ok {
			t.Fatal("RPC value leaked into provider schema")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"result":{"id":"023e105f4ecef8ad9ca31a8372d0c354","type":"A","name":"www.example.com","content":"192.0.2.1","ttl":1,"proxied":true}}`)), Header: make(http.Header)}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	id, err := c.Create(context.Background(), "example.com", dns.Record{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1, Proxied: true})
	if err != nil || id.ID != "023e105f4ecef8ad9ca31a8372d0c354" || id.Name != "www.example.com" || calls != 1 {
		t.Fatal("create failed", err)
	}
}
func TestInvalidRecordNeverReachesTransport(t *testing.T) {
	c, err := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range []string{"example.com", "other.example"} {
		_, err = c.Create(context.Background(), zone, dns.Record{Type: "TXT", Name: "example.com", Value: "v=spf1 -all", TTL: 1, Proxied: true})
		if err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
func TestProviderFailuresAreBoundedAndSecretFree(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		failure bool
	}{
		{"redirect", 302, ``, false}, {"denied", 403, `fixture-not-a-real-credential`, false},
		{"provider", 200, `{"success":false,"errors":[{"message":"fixture-not-a-real-credential"}]}`, false},
		{"malformed", 200, `{`, false}, {"missing ID", 200, `{"success":true,"result":{}}`, false},
		{"large", 200, strings.Repeat("x", (1<<20)+1), false}, {"transport", 0, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c, err := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.failure {
					return nil, errors.New("fixture-not-a-real-credential")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Location": []string{"https://evil.example"}}}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Create(context.Background(), "example.com", dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1})
			if err == nil || strings.Contains(err.Error(), "fixture-not-a-real-credential") || calls != 1 {
				t.Fatal("unsafe failure handling")
			}
		})
	}
}
func TestCanceledContextNeverCallsTransport(t *testing.T) {
	c, err := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Create(ctx, "example.com", dns.Record{Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

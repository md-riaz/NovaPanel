package cloudflare_test

import (
	"context"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"testing"
)

func TestCreateRefusesCallerSuppliedRecordID(t *testing.T) {
	c, err := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Create(context.Background(), "example.com", dns.Record{ID: "023e105f4ecef8ad9ca31a8372d0c354", Type: "A", Name: "example.com", Value: "192.0.2.1", TTL: 1})
	if err == nil {
		t.Fatal("create accepted preassigned provider record ID")
	}
}
func TestConfigurationAndAuthorizedZone(t *testing.T) {
	for _, zones := range []map[string]string{nil, {"bad/path": "023e105f4ecef8ad9ca31a8372d0c353"}, {"example.com": "../evil"}, {"example.com": "023e105f4ecef8ad9ca31a8372d0c353", "EXAMPLE.COM": "023e105f4ecef8ad9ca31a8372d0c354"}} {
		if _, err := cloudflare.New("fixture-not-a-real-credential", zones, nil); err == nil {
			t.Fatal("invalid zone bindings accepted")
		}
	}
	for _, token := range []string{"", "line\r\ninjection", "white space"} {
		if _, err := cloudflare.New(token, map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, nil); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	zones := map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}
	c, err := cloudflare.New("fixture-not-a-real-credential", zones, transport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil }))
	if err != nil {
		t.Fatal(err)
	}
	zones["other.example"] = "023e105f4ecef8ad9ca31a8372d0c354"
	_, err = c.Create(context.Background(), "other.example", dns.Record{Type: "A", Name: "other.example", Value: "192.0.2.1", TTL: 1})
	if err == nil {
		t.Fatal("mutable caller map widened authorized zones")
	}
}

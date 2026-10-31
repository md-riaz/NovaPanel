package cloudflare_test

import (
	"context"
	"io"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"strings"
	"testing"
)

func TestCreateRejectsUnrelatedReturnedRecord(t *testing.T) {
	c, _ := cloudflare.New("fixture", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"result":` + recordJSON + `}`))}, nil
	}))
	if _, e := c.Create(context.Background(), "example.com", dns.Record{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1}); e == nil {
		t.Fatal("unrelated response accepted")
	}
}

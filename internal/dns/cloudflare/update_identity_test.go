package cloudflare_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
)

func TestUpdateRejectsUnrelatedResponseIdentity(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"result":{"id":"023e105f4ecef8ad9ca31a8372d0c354","type":"TXT","name":"other.example.com","content":"fixture","ttl":300,"proxied":false}}`,
		`{"success":true,"result":{"id":"023e105f4ecef8ad9ca31a8372d0c354","type":"A","name":"_dmarc.example.com","content":"192.0.2.1","ttl":300,"proxied":false}}`,
	} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			c, err := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPut {
					t.Fatal("unexpected request method")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			err = c.Update(context.Background(), "example.com", dns.Record{ID: rid, Type: "TXT", Name: "_dmarc.example.com", Value: "fixture", TTL: 300})
			if err == nil {
				t.Fatal("unrelated response identity was accepted")
			}
			if calls != 1 {
				t.Fatalf("update attempted %d requests; want exactly one without retry", calls)
			}
		})
	}
}

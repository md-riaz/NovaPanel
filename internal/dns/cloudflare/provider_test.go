package cloudflare

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"novapanel/internal/dns"
)

func TestProviderCRUD(t *testing.T) {
	p := New("sensitive-token", map[string]string{"example.com": "zone123"})
	p.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer sensitive-token" || !strings.HasPrefix(r.URL.Path, "/client/v4/zones/zone123/dns_records") {
			t.Fatal("bad request")
		}
		result := `{"success":true,"result":{"id":"rec123"}}`
		if r.Method == http.MethodGet {
			result = `{"success":true,"result":[{"id":"rec123","type":"A","name":"www.example.com","content":"192.0.2.1","ttl":1,"proxied":true,"comment":"novapanel:obj_id=one"}]}`
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"comment":"novapanel:obj_id=one"`) || !strings.Contains(string(b), `"proxied":true`) {
				t.Fatal("missing ownership or proxy field")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(result)), Header: make(http.Header)}, nil
	})}
	ctx := context.Background()
	record := dns.Record{Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1, Proxied: true, Comment: "novapanel:obj_id=one"}
	id, err := p.Create(ctx, "example.com", record)
	if err != nil || id != "rec123" {
		t.Fatalf("create: %s %v", id, err)
	}
	records, err := p.List(ctx, "example.com")
	if err != nil || len(records) != 1 || records[0].Comment != record.Comment {
		t.Fatal("list failed")
	}
	record.ID = id
	if err := p.Update(ctx, "example.com", record); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(ctx, "example.com", record); err != nil {
		t.Fatal(err)
	}
}

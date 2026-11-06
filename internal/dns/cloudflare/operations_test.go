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

const rid = "023e105f4ecef8ad9ca31a8372d0c354"
const recordJSON = `{"id":"023e105f4ecef8ad9ca31a8372d0c354","type":"TXT","name":"_dmarc.example.com","content":"fixture","ttl":300,"proxied":false}`

func TestUpdateDeleteList(t *testing.T) {
	calls := 0
	c, e := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.cloudflare.com" {
			t.Fatal("origin")
		}
		body := `{"success":true,"result":` + recordJSON + `}`
		switch calls {
		case 1:
			if r.Method != "PUT" || !strings.HasSuffix(r.URL.Path, "/"+rid) {
				t.Fatal("update")
			}
		case 2:
			if r.Method != "DELETE" {
				t.Fatal("delete")
			}
			body = `{"success":true,"result":{"id":"` + rid + `"}}`
		default:
			if r.Method != "GET" {
				t.Fatal("list")
			}
			body = `{"success":true,"result":[` + recordJSON + `],"result_info":{"page":1,"total_pages":1}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = c.Update(ctx, "example.com", dns.Record{ID: rid, Type: "TXT", Name: "_dmarc.example.com", Value: "fixture", TTL: 300}); e != nil {
		t.Fatal(e)
	}
	if e = c.Delete(ctx, "example.com", rid); e != nil {
		t.Fatal(e)
	}
	rs, e := c.List(ctx, "example.com")
	if e != nil || len(rs) != 1 || rs[0].Name != "_dmarc.example.com" {
		t.Fatal(rs, e)
	}
}

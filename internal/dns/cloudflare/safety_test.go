package cloudflare_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"strings"
	"testing"
)

func TestResponseValidationAndPagination(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"result":[` + strings.ReplaceAll(recordJSON, "_dmarc.example.com", "evil.net") + `],"result_info":{"page":1,"total_pages":1}}`,
		`{"success":true,"result":[` + recordJSON + `,` + recordJSON + `],"result_info":{"page":1,"total_pages":1}}`,
		`{"success":true,"result":[],"result_info":{"page":1,"total_pages":11}}`,
		`{"success":true,"result":[],"result_info":{"page":2,"total_pages":2}}`,
		`{"success":true,"result":null,"result_info":{"page":1,"total_pages":1}}`,
		`{"success":true,"result":[],"result_info":{"page":1,"total_pages":1}} {}`, `{"result":[]}`, `{"success":true,"result":{}}`,
	} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			c, e := cloudflare.New("fixture", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			if e != nil {
				t.Fatal(e)
			}
			if rs, e := c.List(context.Background(), "example.com"); e == nil || rs != nil {
				t.Fatal("unsafe partial result", rs, e)
			}
		})
	}
	calls := 0
	c, _ := cloudflare.New("fixture", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("page") != fmt.Sprint(calls) {
			t.Fatal("page")
		}
		body := fmt.Sprintf(`{"success":true,"result":[],"ignored":"fixture","result_info":{"page":%d,"total_pages":2}}`, calls)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	if rs, e := c.List(context.Background(), "example.com"); e != nil || rs == nil || calls != 2 {
		t.Fatal(rs, e, calls)
	}
}
func TestOperationsRejectUnsafeInputBeforeHTTP(t *testing.T) {
	c, _ := cloudflare.New("fixture", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected HTTP"); return nil, nil }))
	ctx := context.Background()
	if c.Delete(ctx, "example.com", "../evil") == nil {
		t.Fatal("ID")
	}
	if c.Update(ctx, "example.com", dns.Record{ID: rid, Type: "MX", Name: "example.com", Value: "mail.example.com", TTL: 1, Proxied: true}) == nil {
		t.Fatal("proxy")
	}
	for _, u := range []string{"https://evil.net/a", "https://example.com.evil.net/a", "https://user@example.com/a", "file:///example.com", "https://example.com:443/a", "https://example.com/a\n"} {
		if c.Purge(ctx, "example.com", []string{u}) == nil {
			t.Fatal("purge", u)
		}
	}
	if c.Purge(ctx, "example.com", make([]string, 31)) == nil {
		t.Fatal("count")
	}
}
func TestAllOperationsRefuseRedirects(t *testing.T) {
	c, _ := cloudflare.New("fixture", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://evil.net"}}, Body: io.NopCloser(strings.NewReader("fixture"))}, nil
	}))
	ctx := context.Background()
	if c.Delete(ctx, "example.com", rid) == nil || c.Purge(ctx, "example.com", nil) == nil {
		t.Fatal("redirect")
	}
	if _, e := c.List(ctx, "example.com"); e == nil {
		t.Fatal("redirect")
	}
	if c.Update(ctx, "example.com", dns.Record{ID: rid, Type: "TXT", Name: "example.com", Value: "fixture", TTL: 1}) == nil {
		t.Fatal("redirect")
	}
}

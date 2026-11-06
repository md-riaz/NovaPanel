package cloudflare_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"novapanel/internal/dns"
	"novapanel/internal/dns/cloudflare"
	"strings"
	"testing"
)

type resolver struct{ calls int }

func (r *resolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	r.calls++
	return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
}
func TestVerifyAndPurge(t *testing.T) {
	for _, proxied := range []bool{false, true} {
		t.Run(map[bool]string{false: "resolver", true: "provider"}[proxied], func(t *testing.T) {
			c, e := cloudflare.New("fixture-not-a-real-credential", map[string]string{"example.com": "023e105f4ecef8ad9ca31a8372d0c353"}, transport(func(req *http.Request) (*http.Response, error) {
				body := `{"success":true,"result":[{"id":"` + rid + `","type":"A","name":"www.example.com","content":"192.0.2.1","ttl":1,"proxied":` + map[bool]string{false: "false", true: "true"}[proxied] + `}],"result_info":{"page":1,"total_pages":1}}`
				if req.Method == "POST" {
					var b map[string]any
					if json.NewDecoder(req.Body).Decode(&b) != nil || b["purge_everything"] != true || !strings.HasSuffix(req.URL.Path, "/purge_cache") {
						t.Fatal("purge body")
					}
					body = `{"success":true,"result":{"id":"fixture"}}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			}))
			if e != nil {
				t.Fatal(e)
			}
			res := &resolver{}
			v, e := c.Verify(context.Background(), "www.example.com", res, []netip.Addr{netip.MustParseAddr("192.0.2.1")})
			if e != nil || !v.PointsHere || v.Method != map[bool]string{false: "resolver", true: "provider"}[proxied] {
				t.Fatal(v, e)
			}
			if proxied && res.calls != 0 {
				t.Fatal("proxied DNS lookup")
			}
			b, _ := json.Marshal(v)
			var shape map[string]any
			_ = json.Unmarshal(b, &shape)
			if len(shape) != 2 || shape["points_here"] != true {
				t.Fatal(string(b))
			}
			if e = c.Purge(context.Background(), "example.com", nil); e != nil {
				t.Fatal(e)
			}
			var _ dns.Provider = c
		})
	}
}

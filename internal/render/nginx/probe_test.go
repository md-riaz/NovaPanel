package nginx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPProbe(t *testing.T) {
	for _, code := range []int{200, 403, 404, 301, 503} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != "demo.test" {
				t.Errorf("host %s", r.Host)
			}
			if code == 403 && r.URL.Path != "/" {
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(code)
		}))
		e := HTTPProbe(s.URL)(context.Background(), Desired{Domains: []string{"demo.test"}})
		if (e == nil) != (code == 200 || code == 403 || code == 404) {
			t.Fatalf("%d: %v", code, e)
		}
		s.Close()
	}
}

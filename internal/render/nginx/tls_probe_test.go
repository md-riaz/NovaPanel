package nginx

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStateProbeTLSAndCoverage(t *testing.T) {
	d := Desired{Domains: []string{"demo.test", "new.test"}, TLS: installedTLS(t, false), ForceHTTPS: true}
	pair, err := tls.LoadX509KeyPair(d.TLS.certPath(), d.TLS.keyPath())
	if err != nil {
		t.Fatal(err)
	}
	httpsCalls := 0
	secure := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpsCalls++
		if r.Host != "demo.test" || r.TLS.ServerName != "demo.test" {
			t.Errorf("host/SNI %q/%q", r.Host, r.TLS.ServerName)
		}
		w.WriteHeader(403)
	}))
	secure.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	secure.StartTLS()
	defer secure.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/.well-known/") {
			w.WriteHeader(404)
			return
		}
		if r.Host == "demo.test" {
			w.Header().Set("Location", "https://demo.test/")
			w.WriteHeader(301)
			return
		}
		w.WriteHeader(404)
	}))
	defer plain.Close()
	probe := StateProbe(plain.URL, secure.URL, &tls.Config{Time: func() time.Time { return tlsNow }})
	if err := probe(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if httpsCalls != 1 {
		t.Fatalf("HTTPS calls %d", httpsCalls)
	}
	d.TLS = nil
	d.ForceHTTPS = false
	if err := probe(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if httpsCalls != 1 {
		t.Fatal("no-cert HTTPS probe")
	}
}

func TestTLSRenderRefusals(t *testing.T) {
	d := Desired{Name: "demo", Domains: []string{"demo.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", ForceHTTPS: true}
	for _, state := range []*TLSState{nil, {}, installedTLS(t, true)} {
		d.TLS = state
		if _, err := Render(d); err == nil {
			t.Fatal("accepted force HTTPS without production pair")
		}
	}
	d.TLS = installedTLS(t, false)
	d.ForceHTTPS = false
	d.HSTSMaxAge = 600
	if _, err := Render(d); err == nil {
		t.Fatal("HSTS outside forced state")
	}
	d.ForceHTTPS = true
	d.HSTSMaxAge = -1
	if _, err := Render(d); err == nil {
		t.Fatal("negative HSTS")
	}
}

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

func TestStateProbeFailuresAndMaintenance(t *testing.T) {
	for _, tc := range []struct {
		name            string
		page, challenge int
		maintenance, ok bool
	}{
		{"empty docroot", 404, 404, false, true},
		{"forbidden", 403, 200, false, true},
		{"challenge redirect", 200, 301, false, false},
		{"challenge forbidden", 200, 403, false, false},
		{"unexpected 503", 503, 404, false, false},
		{"maintenance", 503, 404, true, true},
		{"maintenance broken challenge", 503, 503, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					w.WriteHeader(tc.page)
				} else {
					w.WriteHeader(tc.challenge)
				}
			}))
			defer s.Close()
			err := StateProbe(s.URL, "", nil)(context.Background(), Desired{Domains: []string{"demo.test"}, Maintenance: tc.maintenance})
			if (err == nil) != tc.ok {
				t.Fatalf("probe %v", err)
			}
		})
	}
	for _, bad := range []string{"http://example.test:80", "http://127.0.0.1:80/path", "http://user@127.0.0.1:80", "https://127.0.0.1:443"} {
		if err := HTTPProbe(bad)(context.Background(), Desired{Domains: []string{"demo.test"}}); err == nil {
			t.Fatalf("accepted endpoint %q", bad)
		}
	}
}

func TestStateProbeVerifiesTLSByDefault(t *testing.T) {
	d := Desired{Domains: []string{"demo.test"}, TLS: installedTLS(t, false)}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer secure.Close()
	if err := StateProbe(plain.URL, secure.URL, nil)(context.Background(), d); err == nil {
		t.Fatal("untrusted server accepted")
	}
	secure.Close()
	if err := StateProbe(plain.URL, secure.URL, &tls.Config{InsecureSkipVerify: true})(context.Background(), d); err == nil {
		t.Fatal("connection failure accepted")
	}
}

func TestCoveredAndUncoveredServerIsolation(t *testing.T) {
	d := Desired{Name: "demo", Domains: []string{"demo.test", "new.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", TLS: installedTLS(t, false), ForceHTTPS: true, HSTSMaxAge: 600}
	b, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	servers := strings.Split(string(b), "server {")[1:]
	if len(servers) != 3 {
		t.Fatalf("servers %d", len(servers))
	}
	if !strings.Contains(servers[0], "server_name new.test;") || strings.Contains(servers[0], "https://") || strings.Contains(servers[0], "443") || strings.Contains(servers[0], "Strict-Transport") {
		t.Fatal(servers[0])
	}
	if !strings.Contains(servers[1], "/.well-known/acme-challenge/") || !strings.Contains(servers[1], "return 301 https://") || strings.Contains(servers[1], "Strict-Transport") {
		t.Fatal(servers[1])
	}
	if !strings.Contains(servers[2], `Strict-Transport-Security "max-age=600"`) || strings.Contains(servers[2], "new.test") {
		t.Fatal(servers[2])
	}
	if _, err := LoadTLS(d.TLS.root, d.TLS.primary, false, tlsNow.Add(2*time.Hour)); err == nil {
		t.Fatal("expired installed pair accepted")
	}
}

package nginx

import (
	"strings"
	"testing"
)

func TestMaintenancePageRenderStates(t *testing.T) {
	for _, state := range []string{"http", "tls", "forced"} {
		t.Run(state, func(t *testing.T) {
			d := Desired{Name: "demo", Domains: []string{"demo.test", "new.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", Maintenance: true, Redirects: []Redirect{{Source: "/old", Target: "/new", Code: 302}}}
			if state != "http" {
				d.TLS = installedTLS(t, false)
			}
			if state == "forced" {
				d.ForceHTTPS = true
				d.HSTSMaxAge = 300
			}
			rendered, err := Render(d)
			if err != nil {
				t.Fatal(err)
			}
			text := string(rendered)
			for _, block := range strings.Split(text, "server {")[1:] {
				acme := strings.Contains(block, "listen 80;")
				if acme {
					start := strings.Index(block, "location ^~ /.well-known/acme-challenge/")
					if start < 0 {
						t.Fatal("missing ACME location")
					}
					challenge := strings.SplitN(block[start:], "}", 2)[0]
					if !strings.Contains(challenge, "try_files $uri =404;") || strings.Contains(challenge, "return ") {
						t.Fatalf("ACME intercepted: %s", challenge)
					}
				}
				if strings.Contains(block, "return 301 https://$host$request_uri;") {
					continue
				}
				for _, want := range []string{"return 503;", "error_page 503 /maintenance.html;", "location = /maintenance.html {", "internal;", `root "/var/www/novapanel";`} {
					if !strings.Contains(block, want) {
						t.Fatalf("missing %q: %s", want, block)
					}
				}
				if !strings.Contains(block, `location = "/old" { return 302 "/new"; }`) {
					t.Fatalf("redirect lost: %s", block)
				}
			}
			if strings.Contains(text, "return 301 https://$host$request_uri;") != d.ForceHTTPS {
				t.Fatal("forced HTTPS changed")
			}
		})
	}
	d := Desired{Name: "demo", Domains: []string{"demo.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo"}
	rendered, err := Render(d)
	if err != nil || strings.Contains(string(rendered), "maintenance.html") {
		t.Fatalf("maintenance leaked into normal render: %s %v", rendered, err)
	}
	d.Maintenance = true
	d.Redirects = []Redirect{{Source: "/maintenance.html", Target: "/new", Code: 302}}
	if _, err := Render(d); err == nil {
		t.Fatal("accepted redirect colliding with internal maintenance page")
	}
}

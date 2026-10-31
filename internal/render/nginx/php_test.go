package nginx

import (
	"novapanel/internal/render/php"
	"strings"
	"testing"
)

func TestPHPRouting(t *testing.T) {
	d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo"}
	b, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `location ~* \.php`) || strings.Contains(string(b), "fastcgi_pass") {
		t.Fatalf("static PHP denial missing: %s", b)
	}
}

func TestPHPDesiredRouting(t *testing.T) {
	d := Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
	for _, maintenance := range []bool{false, true} {
		d.Maintenance = maintenance
		b, err := Render(d)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if maintenance {
			if strings.Contains(s, "fastcgi_pass") {
				t.Fatal(s)
			}
			continue
		}
		for _, want := range []string{"index index.php index.html;", "fastcgi_pass unix:/run/php/novapanel/demo.sock;", "try_files $uri =404;", "fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;", `fastcgi_param PATH_INFO "";`, `location ~* /(?:uploads|files)/.*\.php`, `location ~* \.php`} {
			if !strings.Contains(s, want) {
				t.Fatalf("missing %s: %s", want, s)
			}
		}
		if strings.Index(s, `location ~ /\.`) > strings.Index(s, "fastcgi_pass") {
			t.Fatal("hidden PHP must be denied first")
		}
	}
	for _, mutate := range []func(*php.Desired){func(p *php.Desired) { p.Name = "other" }, func(p *php.Desired) { p.Version = "8.4" }, func(p *php.Desired) { p.UID = 0 }, func(p *php.Desired) { p.Type = "static" }} {
		p := *d.PHP
		mutate(&p)
		d.PHP = &p
		if _, err := Render(d); err == nil {
			t.Fatal("accepted invalid PHP identity")
		}
		d.PHP = &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
	}
}

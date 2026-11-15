package nginx

import (
	"strings"
	"testing"
)

func TestPHPRender(t *testing.T) {
	version := "8.3"
	d := Desired{Name: "demo", Domains: []string{"demo.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHPVersion: &version}
	b, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"index index.php index.html;", "try_files $uri $uri/ /index.php?$query_string;", "location ~ \\.php$", "try_files $uri =404;", "fastcgi_pass unix:/run/php/novapanel/demo.sock;", "include fastcgi_params;", "fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;", "fastcgi_hide_header X-Powered-By;"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Index(s, `location ~ /\.`) > strings.Index(s, `location ~ \.php$`) {
		t.Error("dotfile denial must precede PHP regex")
	}
	for _, bad := range []string{"", "8.2", "8.3; return 200"} {
		d.PHPVersion = &bad
		if _, err := Render(d); err == nil {
			t.Errorf("accepted version %q", bad)
		}
	}
}

func TestPHPHostRedirectMatchesRoot(t *testing.T) {
	version := "8.3"
	for _, maintenance := range []bool{false, true} {
		d := Desired{Name: "demo", Domains: []string{"demo.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHPVersion: &version, Maintenance: maintenance, TLS: installedTLS(t, false), ForceHTTPS: true, Redirects: []Redirect{{Source: "demo.test", Target: "https://other.test/path", Code: 302}}}
		b, err := Render(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range strings.Split(string(b), "server {")[1:] {
			php := strings.SplitN(strings.SplitN(block, `location ~ \.php$ {`, 2)[1], "\n    }", 2)[0]
			if strings.Contains(block, "listen 80;") {
				if !strings.Contains(php, "return 301 https://$host$request_uri;") || strings.Contains(php, `if ($host`) {
					t.Fatalf("HTTPS must precede host redirect: %s", php)
				}
				continue
			}
			redirect := `if ($host = "demo.test") { return 302 "https://other.test/path"; }`
			if !strings.Contains(php, redirect) || strings.Index(php, redirect) > strings.Index(php, "try_files $uri =404;") && !maintenance || maintenance && strings.Index(php, redirect) > strings.Index(php, "return 503;") {
				t.Fatalf("PHP host redirect missing or after handler: %s", php)
			}
			if strings.Index(block, `location ~ /\.`) > strings.Index(block, `location ~ \.php$`) {
				t.Fatal("dotfile denial must precede PHP regex")
			}
		}
	}
}

func TestPHPRoutingRespectsMaintenanceAndHTTPSRedirect(t *testing.T) {
	version := "8.3"
	d := Desired{Name: "demo", Domains: []string{"demo.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHPVersion: &version, Maintenance: true}
	b, err := Render(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "fastcgi_pass") {
		t.Fatal("maintenance bypass through PHP")
	}
	d.Maintenance = false
	d.TLS = installedTLS(t, false)
	d.ForceHTTPS = true
	b, err = Render(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range strings.Split(string(b), "server {")[1:] {
		if strings.Contains(block, "return 301 https://$host$request_uri;") && strings.Contains(block, "fastcgi_pass") {
			t.Fatal("PHP bypasses HTTPS redirect")
		}
	}
}

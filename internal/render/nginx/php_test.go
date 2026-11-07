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

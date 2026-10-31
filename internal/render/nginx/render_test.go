package nginx

import (
	"strings"
	"testing"
)

func TestStaticHTTPRender(t *testing.T) {
	d := Desired{Name: "demo", Domains: []string{"demo.example.test", "www.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo"}
	b, e := Render(d)
	if e != nil {
		t.Fatal(e)
	}
	s := string(b)
	for _, want := range []string{"listen 80;", "server_name demo.example.test www.example.test;", `root "/srv/sites/demo/public";`, `root "/var/www/acme/demo";`, "location ^~ /.well-known/acme-challenge/", "try_files $uri $uri/ =404;", "X-Content-Type-Options"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q: %s", want, s)
		}
	}
	for _, bad := range []string{"443", "ssl_certificate", "fastcgi", "index.php", "https://"} {
		if strings.Contains(s, bad) {
			t.Fatal(s)
		}
	}
	for _, bad := range []string{"", "bad name", "evil;include", "$host", "../demo", "root"} {
		v := d
		v.Name = bad
		if _, e = Render(v); e == nil {
			t.Fatalf("name %q", bad)
		}
	}
	for _, bad := range []string{"", "bad name", "x;return", "$host", "../demo"} {
		v := d
		v.Domains = []string{bad}
		if _, e = Render(v); e == nil {
			t.Fatalf("domain %q", bad)
		}
	}
	for _, bad := range []string{"", "relative", "/srv/../etc", "/srv/a b", "/srv/$root", "/srv/a;return", "/srv/\"oops"} {
		v := d
		v.Docroot = bad
		if _, e = Render(v); e == nil {
			t.Fatalf("path %q", bad)
		}
		v = d
		v.ACMERoot = bad
		if _, e = Render(v); e == nil {
			t.Fatalf("acme %q", bad)
		}
	}
	d.Domains = nil
	if _, e = Render(d); e == nil {
		t.Fatal("empty domains")
	}
}

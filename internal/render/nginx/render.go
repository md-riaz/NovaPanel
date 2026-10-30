// Package nginx renders structured site state. It supports HTTP and installed TLS static sites.
package nginx

import (
	"bytes"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"path/filepath"
	"strings"
	"text/template"
)

type Redirect struct {
	Source, Target string
	Code           int
}
type Desired struct {
	ForceReload       bool
	TLS               *TLSState
	ForceHTTPS        bool
	HSTSMaxAge        int64
	ModernHTTP2       bool
	Maintenance       bool
	Redirects         []Redirect
	Name              string
	Domains           []string
	Docroot, ACMERoot string
}

type serverState struct {
	Desired
	HTTPS, RedirectHTTPS     bool
	CertificatePath, KeyPath string
	HSTSAge                  int64
}

var httpTemplate = template.Must(template.New("http").Funcs(template.FuncMap{"pathSource": func(s string) bool { return strings.HasPrefix(s, "/") }}).Parse(`server {
    {{if .HTTPS}}{{if .ModernHTTP2}}listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;{{else}}listen 443 ssl http2;
    listen [::]:443 ssl http2;{{end}}
    ssl_certificate "{{.CertificatePath}}";
    ssl_certificate_key "{{.KeyPath}}";{{else}}listen 80;
    listen [::]:80;{{end}}
    server_name {{range .Domains}}{{.}} {{end}};
    root "{{.Docroot}}";
    index index.html;
    {{if not .HTTPS}}location ^~ /.well-known/acme-challenge/ {
        root "{{.ACMERoot}}";
        default_type "text/plain";
        try_files $uri =404;
    }
    {{end}}{{if .RedirectHTTPS}}location / { return 301 https://$host$request_uri; }
    {{else}}{{range .Redirects}}{{if pathSource .Source}}location = "{{.Source}}" { return {{.Code}} "{{.Target}}"; }
    {{end}}{{end}}location / {
        {{range .Redirects}}{{if not (pathSource .Source)}}if ($host = "{{.Source}}") { return {{.Code}} "{{.Target}}"; }
        {{end}}{{end}}{{if .Maintenance}}return 503;{{else}}try_files $uri $uri/ =404;{{end}}
    }
    {{end}}location ~ /\. { deny all; }
    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options SAMEORIGIN always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;
    {{if .HSTSAge}}add_header Strict-Transport-Security "max-age={{.HSTSAge}}" always;{{end}}
}
`))

func Render(d Desired) ([]byte, error) {
	v := &validate.V{}
	if e := v.SiteName(d.Name); e != nil {
		return nil, e
	}
	if len(d.Domains) == 0 {
		return nil, rpcerr.Errf("invalid_argument", "Site requires a domain")
	}
	for _, name := range d.Domains {
		if e := v.Hostname(name); e != nil {
			return nil, e
		}
	}
	paths := []string{d.Docroot, d.ACMERoot}
	if d.TLS != nil {
		paths = append(paths, d.TLS.root)
	}
	for _, p := range paths {
		// Nginx quoted paths still expand variables; allow only literal path bytes.
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return nil, rpcerr.Errf("invalid_argument", "Invalid nginx path")
		}
		for _, c := range p {
			if !(c == '/' || c == '-' || c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return nil, rpcerr.Errf("invalid_argument", "Invalid nginx path")
			}
		}
	}
	seen := map[string]bool{}
	for _, r := range d.Redirects {
		if e := v.RedirectSource(r.Source); e != nil {
			return nil, e
		}
		if e := v.RedirectTarget(r.Target); e != nil {
			return nil, e
		}
		if (r.Code != 301 && r.Code != 302) || seen[r.Source] {
			return nil, rpcerr.Errf("invalid_argument", "Invalid or duplicate redirect")
		}
		seen[r.Source] = true
	}
	if d.HSTSMaxAge < 0 {
		return nil, rpcerr.Errf("invalid_argument", "Invalid HSTS lifetime")
	}
	if d.TLS != nil && !d.TLS.covers(d.TLS.primary) {
		return nil, rpcerr.Errf("precondition_failed", "Valid installed certificate required")
	}
	if d.ForceHTTPS && (d.TLS == nil || d.TLS.staging) || d.HSTSMaxAge > 0 && !d.ForceHTTPS {
		return nil, rpcerr.Errf("precondition_failed", "Production certificate and forced HTTPS required")
	}
	var covered, uncovered []string
	for _, host := range d.Domains {
		if d.TLS.covers(host) {
			covered = append(covered, host)
		} else {
			uncovered = append(uncovered, host)
		}
	}
	if d.TLS != nil && len(covered) == 0 {
		return nil, rpcerr.Errf("precondition_failed", "Certificate covers no site domains")
	}
	var servers []serverState
	if len(uncovered) > 0 {
		v := d
		v.Domains = uncovered
		servers = append(servers, serverState{Desired: v})
	}
	if len(covered) > 0 {
		v := d
		v.Domains = covered
		servers = append(servers, serverState{Desired: v, RedirectHTTPS: d.ForceHTTPS})
		age := int64(0)
		if d.ForceHTTPS {
			age = d.HSTSMaxAge
		}
		servers = append(servers, serverState{Desired: v, HTTPS: true, CertificatePath: d.TLS.certPath(), KeyPath: d.TLS.keyPath(), HSTSAge: age})
	}
	var b bytes.Buffer
	for _, server := range servers {
		if e := httpTemplate.Execute(&b, server); e != nil {
			return nil, e
		}
	}
	return []byte(strings.ReplaceAll(b.String(), " ;", ";")), nil
}

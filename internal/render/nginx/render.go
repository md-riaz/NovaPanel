// Package nginx renders structured site state. This first slice supports HTTP static sites only.
package nginx

import (
	"bytes"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"path/filepath"
	"strings"
	"text/template"
)

type Desired struct {
	Name              string
	Domains           []string
	Docroot, ACMERoot string
}

var httpTemplate = template.Must(template.New("http").Parse(`server {
    listen 80;
    listen [::]:80;
    server_name {{range .Domains}}{{.}} {{end}};
    root "{{.Docroot}}";
    index index.html;
    location ^~ /.well-known/acme-challenge/ {
        root "{{.ACMERoot}}";
        default_type "text/plain";
        try_files $uri =404;
    }
    location / { try_files $uri $uri/ =404; }
    location ~ /\. { deny all; }
    add_header X-Content-Type-Options nosniff always;
    add_header X-Frame-Options SAMEORIGIN always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;
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
	for _, p := range []string{d.Docroot, d.ACMERoot} {
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
	var b bytes.Buffer
	if e := httpTemplate.Execute(&b, d); e != nil {
		return nil, e
	}
	return []byte(strings.ReplaceAll(b.String(), " ;", ";")), nil
}

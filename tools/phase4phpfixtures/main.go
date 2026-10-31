// Command phase4phpfixtures emits nonsecret exact production renderer bytes.
package main

import (
	"encoding/json"
	"novapanel/internal/render/nginx"
	"novapanel/internal/render/php"
	"os"
)

func main() {
	p := php.Desired{Name: "lab-site", Type: "php", Version: "8.3", UID: 33, GID: 33}
	pool, err := php.Render(p)
	if err != nil {
		panic(err)
	}
	site, err := nginx.Render(nginx.Desired{Name: p.Name, Domains: []string{"lab.invalid"}, Docroot: "/tmp/lab/public", ACMERoot: "/tmp/lab/acme", PHP: &p})
	if err != nil {
		panic(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(struct{ Nginx, Pool string }{string(site), string(pool)}); err != nil {
		panic(err)
	}
}

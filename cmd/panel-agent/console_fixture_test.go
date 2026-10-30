package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/render/nginx"
)

// This fixture never delegates commands to the host. Filesystem ownership changes
// still run, but only against the disposable, explicitly supplied site roots.
type consoleRunner struct{ sites string }

func (r consoleRunner) Run(ctx context.Context, prog string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" && reflect.DeepEqual(args, []string{"passwd", "browserdemo"}) {
		return []byte(fmt.Sprintf("browserdemo:x:65534:65534::%s:/usr/sbin/nologin\n", filepath.Join(r.sites, "browserdemo"))), nil, 0, nil
	}
	if prog == "/usr/bin/getent" && reflect.DeepEqual(args, []string{"group", "www-data"}) {
		return []byte("www-data:x:65534:\n"), nil, 0, nil
	}
	if prog == "/usr/sbin/nginx" && reflect.DeepEqual(args, []string{"-t"}) {
		return nil, nil, 0, nil
	}
	if prog == "/usr/bin/systemctl" && reflect.DeepEqual(args, []string{"reload", "nginx"}) {
		return nil, nil, 0, nil
	}
	return (safeRunner{}).Run(ctx, prog, args, opts)
}
func configureConsoleFixture(t *testing.T, a *agent.Agent) {
	t.Helper()
	root := filepath.Dir(a.Cfg.SocketPath)
	for _, path := range []string{a.Cfg.DBPath, a.Cfg.SealKeyPath, a.Cfg.LogDir, a.Cfg.ACMEWebroot, a.Cfg.SitesRoot, a.Cfg.TLSDir} {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			t.Fatal("console fixture path outside scratch")
		}
	}
	// Match the installer-owned private TLS root; publication refuses absent roots.
	if err := os.Mkdir(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	r := consoleRunner{sites: a.Cfg.SitesRoot}
	a.Exec = r
	a.ProvisionSite = func(ctx context.Context, name string) error {
		return platform.EnsureSite(ctx, r, a.Cfg.SitesRoot, a.Cfg.ACMEWebroot, name)
	}
	ng := filepath.Join(root, "nginx")
	for _, path := range []string{a.Cfg.SitesRoot, a.Cfg.ACMEWebroot, filepath.Join(ng, "sites-available"), filepath.Join(ng, "sites-enabled")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Service probe is injected too: no host HTTP/HTTPS listeners are contacted.
	a.Nginx.Root = ng
	a.Nginx.Runner = r
	a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
}

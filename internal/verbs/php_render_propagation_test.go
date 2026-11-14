package verbs_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestPHPVersionSurvivesSiteUpdateAndTLSPublication(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	if err = os.Mkdir(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	ng := filepath.Join(root, "nginx")
	for _, d := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(ng, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	a.Nginx.Root = ng
	a.Nginx.Runner = &publicationRunner{}
	a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
	sid, _ := id.New()
	did, _ := id.New()
	version := "8.3"
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	run := func(verb string, args any) {
		t.Helper()
		raw, _ := json.Marshal(args)
		reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: raw})
		if !reply.OK {
			t.Fatalf("%s: %+v", verb, reply)
		}
		j, e := st.StartJob(ctx, reply.Data.(jobs.EnqueueResult).Job.ID, a.Now())
		if e != nil {
			t.Fatal(e)
		}
		if e = server.ExecuteJob(ctx, &j); e != nil {
			t.Fatal(e)
		}
		config, e := os.ReadFile(filepath.Join(ng, "sites-available", "demo.conf"))
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(string(config), "fastcgi_pass unix:/run/php/novapanel/demo.sock;") {
			t.Fatalf("%s lost PHP routing: %s", verb, config)
		}
	}
	run("site.update", map[string]any{"site_id": sid, "maintenance": false})
	cert, key := uploadPEM(t, 123)
	run("ssl.upload", map[string]any{"site_id": sid, "cert_pem": cert, "key_pem": key})
}

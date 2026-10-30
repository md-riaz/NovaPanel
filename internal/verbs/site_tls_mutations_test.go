package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

func TestTLSAwareSiteMutations(t *testing.T) {
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
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
	if err = os.Mkdir(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	ng := filepath.Join(root, "nginx")
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(ng, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	commands := &publicationRunner{}
	a.Nginx = &nginx.Applier{Root: ng, Runner: commands, Probe: func(context.Context, nginx.Desired) error { return nil }}
	sid, _ := id.New()
	did, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	cp, kp := uploadPEM(t, 1)
	certificate, err := certs.Validate([]byte(cp), []byte(kp), []string{"demo.test"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	token, err := certs.Stage(a.Cfg.TLSDir, "demo.test", certificate)
	if err != nil {
		t.Fatal(err)
	}
	if err = certs.Publish(token); err != nil {
		t.Fatal(err)
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sites SET cert_source='manual' WHERE id=?", sid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	call := func(verb string, args any) rpc.Reply {
		raw, _ := json.Marshal(args)
		return server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: raw})
	}
	exec := func(verb string, args any) jobs.Job {
		t.Helper()
		r := call(verb, args)
		if !r.OK {
			t.Fatalf("%s admission: %+v", verb, r)
		}
		j := jobs.Job(r.Data.(jobs.EnqueueResult).Job)
		if e := rpc.Registry[verb].Exec(ctx, a, &j); e != nil {
			t.Fatalf("%s execution: %v", verb, e)
		}
		return j
	}
	config := func() string {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(ng, "sites-available/demo.conf"))
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	exec("site.update", map[string]any{"site_id": sid, "maintenance": true})
	if b := config(); !strings.Contains(b, "listen 443 ssl") || !strings.Contains(b, "return 503") {
		t.Fatalf("TLS maintenance config: %s", b)
	}
	exec("site.update", map[string]any{"site_id": sid, "maintenance": false, "force_https": true, "hsts_max_age": 300})
	exec("redirect.add", map[string]any{"site_id": sid, "source": "/old", "target": "/new", "code": 301})
	if b := config(); !strings.Contains(b, "Strict-Transport-Security") || !strings.Contains(b, "return 301 \"/new\"") {
		t.Fatalf("TLS redirect config: %s", b)
	}
	j := exec("domain.add", map[string]any{"site_id": sid, "fqdn": "alias.test"})
	var added struct {
		ID string `json:"domain_id"`
	}
	if err = json.Unmarshal(j.Result, &added); err != nil {
		t.Fatal(err)
	}
	b := config()
	parts := strings.Split(b, "server {")
	for _, part := range parts {
		if strings.Contains(part, "server_name alias.test") && (strings.Contains(part, "listen 443") || strings.Contains(part, "https://$host")) {
			t.Fatalf("uncovered alias got HTTPS: %s", part)
		}
	}
	if !strings.Contains(b, "server_name alias.test") || !strings.Contains(b, "listen 443 ssl") {
		t.Fatalf("lost HTTP alias or TLS primary: %s", b)
	}
	exec("site.update", map[string]any{"site_id": sid, "primary_domain_id": did})
	if r := call("site.update", map[string]any{"site_id": sid, "primary_domain_id": added.ID}); r.Code != "precondition_failed" {
		t.Fatalf("TLS rekey must fail closed: %+v", r)
	}
	exec("site.update", map[string]any{"site_id": sid, "force_https": false, "hsts_max_age": 0})
	if r := call("site.update", map[string]any{"site_id": sid, "force_https": true}); r.Code != "precondition_failed" {
		t.Fatalf("initial enable allowed uncovered domain: %+v", r)
	}
	exec("domain.remove", map[string]any{"domain_id": added.ID})
	exec("site.update", map[string]any{"site_id": sid, "force_https": true})
	// Provenance and real pair checks must run again when queued updates execute.
	queued := call("site.update", map[string]any{"site_id": sid, "maintenance": true})
	if !queued.OK {
		t.Fatal(queued)
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE domains SET cert_staging=1 WHERE site_id=?", sid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	pending := jobs.Job(queued.Data.(jobs.EnqueueResult).Job)
	before := config()
	if err = rpc.Registry["site.update"].Exec(ctx, a, &pending); err == nil {
		t.Fatal("queued update accepted staging forced HTTPS")
	}
	if config() != before {
		t.Fatal("refused update changed config")
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE domains SET cert_staging=0 WHERE site_id=?", sid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(a.Cfg.TLSDir, "demo.test", "current")); err != nil {
		t.Fatal(err)
	}
	if r := call("site.update", map[string]any{"site_id": sid, "maintenance": true}); r.Code != "precondition_failed" {
		t.Fatalf("missing pair admitted: %+v", r)
	}
	if config() != before {
		t.Fatal("missing pair changed config")
	}
}

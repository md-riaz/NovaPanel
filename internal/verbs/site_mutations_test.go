package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestSiteMutations(t *testing.T) {
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
	a.Cfg.SitesRoot = filepath.Join(root, "sites")
	a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
	for _, p := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(root, "nginx", p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	commands := &siteRunner{}
	a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: commands, Probe: func(context.Context, nginx.Desired) error { return nil }}
	sid, _ := id.New()
	did, _ := id.New()
	site, err := st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: filepath.Join(a.Cfg.SitesRoot, "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	call := func(verb string, args any) rpc.Reply {
		b, _ := json.Marshal(args)
		return s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: b})
	}
	exec := func(verb string, args any) store.Job {
		t.Helper()
		r := call(verb, args)
		if !r.OK {
			t.Fatalf("%s: %+v", verb, r)
		}
		en := r.Data.(jobs.EnqueueResult)
		if en.Job.ObjectKey != "site:"+sid {
			t.Fatalf("wrong lock %+v", en.Job)
		}
		j := jobs.Job(en.Job)
		if e := rpc.Registry[verb].Exec(ctx, a, &j); e != nil {
			t.Fatalf("%s exec: %v", verb, e)
		}
		first := commands.calls
		retry := jobs.Job(en.Job)
		if e := rpc.Registry[verb].Exec(ctx, a, &retry); e != nil {
			t.Fatalf("%s retry: %v", verb, e)
		}
		if commands.calls != first {
			t.Fatalf("%s retry reloaded", verb)
		}
		return store.Job(j)
	}
	exec("site.update", map[string]any{"site_id": sid, "maintenance": true})
	got, _ := st.SiteByID(ctx, sid)
	if !got.Maintenance {
		t.Fatal("maintenance not persisted")
	}
	exec("site.update", map[string]any{"site_id": sid, "maintenance": false})
	j := exec("domain.add", map[string]any{"site_id": sid, "fqdn": "alias.test"})
	var added struct {
		ID string `json:"domain_id"`
	}
	if err = json.Unmarshal(j.Result, &added); err != nil || added.ID == "" {
		t.Fatalf("domain result %s", j.Result)
	}
	if r := call("domain.remove", map[string]any{"domain_id": did}); r.Code != "precondition_failed" {
		t.Fatalf("primary removal %+v", r)
	}
	exec("domain.remove", map[string]any{"domain_id": added.ID})
	j = exec("redirect.add", map[string]any{"site_id": sid, "source": "/old", "target": "/new", "code": 301})
	var redirect struct {
		ID string `json:"redirect_id"`
	}
	json.Unmarshal(j.Result, &redirect)
	bytes, err := os.ReadFile(filepath.Join(root, "nginx", "sites-available", "demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(bytes), "return 301 \"/new\";") {
		t.Fatalf("redirect not rendered %s", bytes)
	}
	exec("redirect.remove", map[string]any{"redirect_id": redirect.ID})
	if r := call("site.update", map[string]any{"site_id": sid, "force_https": true}); r.Code != "precondition_failed" {
		t.Fatalf("TLS gate %+v", r)
	}
	if r := call("redirect.add", map[string]any{"site_id": sid, "source": "/x;evil", "target": "/new", "code": 301}); r.Code != "invalid_argument" {
		t.Fatalf("source validation %+v", r)
	}
	if err = st.SetSiteStatus(ctx, sid, "failed"); err != nil {
		t.Fatal(err)
	}
	r := call("site.delete", map[string]any{"site_id": sid})
	if !r.OK {
		t.Fatalf("delete admission %+v", r)
	}
	en := r.Data.(jobs.EnqueueResult)
	dj := jobs.Job(en.Job)
	// Cleanup failure must retain deleting metadata for a retry.
	a.Nginx = nil
	if err = rpc.Registry["site.delete"].Exec(ctx, a, &dj); err == nil {
		t.Fatal("cleanup unexpectedly succeeded")
	}
	got, err = st.SiteByID(ctx, sid)
	if err != nil || got.Status != "deleting" {
		t.Fatalf("delete lifecycle %+v %v", got, err)
	}
	a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: commands, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	if err = os.MkdirAll(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = rpc.Registry["site.delete"].Exec(ctx, a, &dj); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SiteByID(ctx, sid); err == nil {
		t.Fatal("site remains")
	}
	if err = rpc.Registry["site.delete"].Exec(ctx, a, &dj); err != nil {
		t.Fatalf("delete retry %v", err)
	}
	_ = site
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

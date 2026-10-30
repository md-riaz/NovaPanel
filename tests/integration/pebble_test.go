package integration

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealPebbleNginxJobs(t *testing.T) {
	if os.Getenv("NOVAPANEL_PEBBLE") != "1" {
		t.Skip("isolated Pebble lab only")
	}
	l, ap := newNginxLab(t)
	ctx := context.Background()
	st, err := store.Open(l.root + "/panel.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, l.root+"/audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Nginx = ap
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a.Cfg.TLSDir = l.root + "/tls"
	a.Cfg.ACMEWebroot = l.root + "/webroot"
	a.Cfg.ACMEDirectoryStaging = "https://localhost:14000/dir"
	a.Cfg.ACMEUseStaging = true
	if err = os.MkdirAll(a.Cfg.ACMEWebroot+"/demo", 0755); err != nil {
		t.Fatal(err)
	}
	sid, _ := id.New()
	did, _ := id.New()
	if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: l.root + "/public"}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	workerctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(workerctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	dispatch := func(verb string, args any) rpc.Reply {
		b, _ := json.Marshal(args)
		return s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: b})
	}
	run := func(verb string, args any) jobs.Job {
		t.Helper()
		r := dispatch(verb, args)
		if !r.OK {
			t.Fatalf("%s admission: %+v", verb, r)
		}
		enq, ok := r.Data.(jobs.EnqueueResult)
		if !ok {
			t.Fatalf("%s not queued: %+v", verb, r)
		}
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			j, e := st.GetJob(ctx, enq.Job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if j.State == "failed" {
				t.Fatalf("%s job: %+v", verb, j)
			}
			if j.State == "done" {
				t.Logf("job %s completed result=%s", verb, j.Result)
				return j
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("worker timeout")
		return jobs.Job{}
	}
	args := map[string]any{"site_id": sid}
	wildcard := dispatch("ssl.issue", map[string]any{"site_id": sid, "wildcard": true})
	t.Logf("canonical wildcard:true probe returned ok=%v code=%s; required unsupported", wildcard.OK, wildcard.Code)
	run("site.update", map[string]any{"site_id": sid, "maintenance": false})
	labGet(t, "/", 200, "phase3-real-nginx")
	run("ssl.issue", args)
	first, e := os.Readlink(a.Cfg.TLSDir + "/demo.test/current")
	if e != nil {
		t.Fatal(e)
	}
	pair, e := certs.Load(a.Cfg.TLSDir, "demo.test", []string{"demo.test"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(a.Cfg.TLSDir, "demo.test", first, "privkey.pem")); e != nil {
		t.Fatal(e)
	}
	t.Logf("Pebble issued version=%s expires=%s", first, pair.NotAfter)
	before := l.reloads
	r := dispatch("ssl.issue", args)
	if !r.OK {
		t.Fatal(r)
	}
	if _, ok := r.Data.(jobs.EnqueueResult); ok {
		t.Fatal("duplicate issue queued")
	}
	if l.reloads != before {
		t.Fatal("no-op reloaded")
	}
	r = dispatch("site.update", map[string]any{"site_id": sid, "force_https": true})
	if r.OK || r.Code != "precondition_failed" {
		t.Fatalf("staging force HTTPS not refused %+v", r)
	}
	run("ssl.renew", args)
	second, e := os.Readlink(a.Cfg.TLSDir + "/demo.test/current")
	if e != nil || second == first {
		t.Fatal("renewal did not publish new version", e)
	}
	if l.reloads <= before {
		t.Fatal("renewal did not reload")
	}
	run("domain.add", map[string]any{"site_id": sid, "fqdn": "alias.test"})
	run("ssl.renew", args)
	if _, e = certs.Load(a.Cfg.TLSDir, "demo.test", []string{"demo.test", "alias.test"}, time.Now()); e != nil {
		t.Fatal(e)
	}
	labGet(t, "/.well-known/acme-challenge/missing", 404, "")
	ds, e := st.ListDomains(ctx, sid)
	if e != nil || len(ds) != 2 {
		t.Fatal(ds, e)
	}
	for _, d := range ds {
		if d.CertState != "issued" || !d.CertStaging {
			t.Fatalf("coverage %+v", d)
		}
	}
	intent, e := st.TLSIntent(ctx)
	if e != nil || len(intent) != 0 {
		t.Fatal("remaining publication intent", e)
	}
	t.Log("real Pebble + lego + HTTP01 served by real nginx + production queue issuance/noop/renewal/SAN expansion passed; staging force-HTTPS refused")
}

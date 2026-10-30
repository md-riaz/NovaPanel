package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type acmeTestRunner struct{ reloads int }

func (r *acmeTestRunner) Run(_ context.Context, _ string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if args[0] == "reload" {
		r.reloads++
	}
	return nil, nil, 0, nil
}

func TestACMEIssueRenewPublication(t *testing.T) {
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
	a.Secret, _ = secret.New(make([]byte, 32))
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
	for _, p := range []string{a.Cfg.TLSDir, filepath.Join(a.Cfg.ACMEWebroot, "demo"), filepath.Join(root, "nginx/sites-available"), filepath.Join(root, "nginx/sites-enabled")} {
		if err = os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	os.Chmod(a.Cfg.TLSDir, 0700)
	f := newFakeCA(t, filepath.Join(a.Cfg.ACMEWebroot, "demo"))
	a.Cfg.ACMEDirectoryStaging = f.server.URL + "/directory"
	a.Nginx.Root = filepath.Join(root, "nginx")
	r := &acmeTestRunner{}
	a.Nginx.Runner = r
	a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
	sid, _ := id.New()
	did, _ := id.New()
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	st.SetSiteStatus(ctx, sid, "active")
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	run := func(verb string) *jobs.Job {
		args := json.RawMessage(`{"site_id":"` + sid + `","staging":true}`)
		if verb == "ssl.renew" {
			args = json.RawMessage(`{"site_id":"` + sid + `"}`)
		}
		res := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: args})
		if !res.OK {
			t.Fatal(res)
		}
		enq, ok := res.Data.(jobs.EnqueueResult)
		if !ok {
			t.Fatalf("expected job: %+v", res)
		}
		j, e := st.StartJob(ctx, enq.Job.ID, a.Now())
		if e != nil {
			t.Fatal(e)
		}
		if verb == "ssl.renew" {
			a.Nginx.Probe = func(context.Context, nginx.Desired) error { panic("simulated process interruption") }
			if e = (acmeExecutor{a}).execute(ctx, &j, verb, f.server.Client()); e == nil {
				t.Fatal("crash not reported")
			}
			pending, e := st.TLSIntent(ctx)
			if e != nil || len(pending) == 0 {
				t.Fatalf("lost recovery intent %v", e)
			}
			a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
		}
		if e = (acmeExecutor{a}).execute(ctx, &j, verb, f.server.Client()); e != nil {
			t.Fatal(e)
		}
		beforeReplay := r.reloads
		if e = (acmeExecutor{a}).execute(ctx, &j, verb, f.server.Client()); e != nil || r.reloads != beforeReplay {
			t.Fatalf("committed replay reloaded: %v", e)
		}
		j.State = "done"
		if e = st.UpdateJob(ctx, j); e != nil {
			t.Fatal(e)
		}
		return &j
	}
	j := run("ssl.issue")
	first, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "example.test/current"))
	site, _ := st.SiteByID(ctx, sid)
	ds, _ := st.ListDomains(ctx, sid)
	if site.CertSource != "letsencrypt" || !ds[0].CertStaging || ds[0].CertState != "issued" {
		t.Fatalf("metadata %+v %+v", site, ds)
	}
	if _, err = certs.Load(a.Cfg.TLSDir, "example.test", []string{"example.test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	noop := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "ssl.issue", Args: json.RawMessage(`{"site_id":"` + sid + `","staging":true}`)})
	if !noop.OK {
		t.Fatal(noop)
	}
	if _, ok := noop.Data.(jobs.EnqueueResult); !ok {
		t.Fatal("valid issue omitted job envelope")
	}
	if err = (acmeExecutor{a}).execute(ctx, j, "ssl.issue", f.server.Client()); err == nil {
		t.Fatal("terminal job accepted")
	}
	run("ssl.renew")
	second, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "example.test/current"))
	if first == second || r.reloads != 4 {
		t.Fatalf("publication first=%s second=%s reloads=%d", first, second, r.reloads)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.orders != 2 || f.registrations != 1 || f.replacements[1] == "" {
		t.Fatalf("orders=%d registrations=%d replacements=%v", f.orders, f.registrations, f.replacements)
	}
}

func TestACMEExecutionRequiresDurableParentBeforeCA(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := agent.New()
	a.Store = st
	j := &jobs.Job{ID: "missing", Verb: "ssl.issue", ObjectKey: "site:missing", Args: []byte(`{"site_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","staging":true}`)}
	err = (acmeExecutor{a}).execute(context.Background(), j, "ssl.issue", nil)
	if err == nil {
		t.Fatal("missing durable job accepted")
	}
}

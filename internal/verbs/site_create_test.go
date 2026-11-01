package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type siteRunner struct{ calls int }

func (r *siteRunner) Run(context.Context, string, []string, runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	return nil, nil, 0, nil
}

func TestQueuedSiteCreate(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			st, e := store.Open(filepath.Join(root, "panel.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			log, e := audit.Open(st, filepath.Join(root, "audit.log"))
			if e != nil {
				t.Fatal(e)
			}
			defer log.Close()
			a := agent.New()
			a.Store = st
			a.Audit = log
			a.Cfg.SitesRoot = filepath.Join(root, "sites")
			a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
			for _, p := range []string{a.Cfg.SitesRoot, a.Cfg.ACMEWebroot, filepath.Join(root, "nginx", "sites-available"), filepath.Join(root, "nginx", "sites-enabled")} {
				if e = os.MkdirAll(p, 0755); e != nil {
					t.Fatal(e)
				}
			}
			provisioned := 0
			a.ProvisionSite = func(context.Context, string) error {
				provisioned++
				if fail {
					return errors.New("injected provision failure")
				}
				return nil
			}
			commands := &siteRunner{}
			a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: commands, Probe: func(context.Context, nginx.Desired) error { return nil }}
			s := rpc.NewServer(a)
			s.ConfigureJobs()
			call := func(args string) rpc.Reply {
				return s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: json.RawMessage(args)})
			}
			for _, args := range []string{`{"name":"bad;name","type":"static","primary_domain":"demo.test"}`, `{"name":"demo","type":"static","primary_domain":"bad;domain"}`} {
				if r := call(args); r.Code != "invalid_argument" {
					t.Fatalf("validation %+v", r)
				}
			}
			if r := call(`{"name":"demo","type":"php","primary_domain":"demo.test"}`); r.Code != "unsupported" {
				t.Fatal(r)
			}
			r := call(`{"name":"demo","type":"static","primary_domain":"demo.test"}`)
			if !r.OK {
				t.Fatal(r)
			}
			en := r.Data.(jobs.EnqueueResult)
			site, e := st.SiteByName(ctx, "demo")
			if e != nil {
				t.Fatal(e)
			}
			if site.Status != "creating" || en.Job.ObjectKey != "site:demo" || en.Job.Effectful || provisioned != 0 || commands.calls != 0 {
				t.Fatalf("admission %+v %+v", site, en.Job)
			}
			run, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(run) }()
			defer func() {
				cancel()
				if e := <-done; e != nil {
					t.Error(e)
				}
			}()
			wait := func(jobID string) store.Job {
				t.Helper()
				until := time.Now().Add(3 * time.Second)
				for {
					j, e := st.GetJob(ctx, jobID)
					if e != nil {
						t.Fatal(e)
					}
					if j.State == "done" || j.State == "failed" {
						return j
					}
					if time.Now().After(until) {
						t.Fatal("worker timed out")
					}
					time.Sleep(time.Millisecond)
				}
			}
			j := wait(en.Job.ID)
			site, e = st.SiteByID(ctx, site.ID)
			if e != nil {
				t.Fatal(e)
			}
			if fail {
				if site.Status != "failed" || j.State != "failed" {
					t.Fatalf("%+v %+v", site, j)
				}
				fail = false
				r = call(`{"name":"demo","type":"static","primary_domain":"demo.test"}`)
				if !r.OK {
					t.Fatal(r)
				}
				j = wait(r.Data.(jobs.EnqueueResult).Job.ID)
				site, e = st.SiteByID(ctx, site.ID)
				if e != nil || site.Status != "active" || j.State != "done" {
					t.Fatalf("failed creation did not roll forward: %+v %+v %v", site, j, e)
				}
				return
			}
			if site.Status != "active" || j.State != "done" || string(j.Result) != `{"site_id":"`+site.ID+`"}` {
				t.Fatalf("%+v %+v", site, j)
			}
			first := commands.calls
			r = call(`{"name":"demo","type":"static","primary_domain":"demo.test"}`)
			if !r.OK {
				t.Fatal(r)
			}
			j = wait(r.Data.(jobs.EnqueueResult).Job.ID)
			if j.State != "done" || commands.calls != first {
				t.Fatalf("retry reloaded %+v %d/%d", j, commands.calls, first)
			}
			if _, e = os.Stat(filepath.Join(root, "nginx", "sites-available", "demo.conf.lkg")); e != nil {
				t.Fatal(e)
			}
			for _, state := range []string{"maintenance", "redirect", "tls"} {
				t.Run("active_retry_"+state, func(t *testing.T) {
					if err := st.SetSiteStatus(ctx, site.ID, "active"); err != nil {
						t.Fatal(err)
					}
					no := false
					if err := st.UpdateSite(ctx, site.ID, store.SitePatch{Maintenance: &no}); err != nil {
						t.Fatal(err)
					}
					if err := st.RemoveRedirect(ctx, "redirect"); err != nil {
						t.Fatal(err)
					}
					switch state {
					case "maintenance":
						yes := true
						e = st.UpdateSite(ctx, site.ID, store.SitePatch{Maintenance: &yes})
					case "redirect":
						_, e = st.AddRedirect(ctx, store.Redirect{ID: "redirect", SiteID: site.ID, Source: "/old", Target: "/new", Code: 301})
					case "tls":
						e = st.Tx(ctx, func(tx *sql.Tx) error {
							if _, err := tx.ExecContext(ctx, "UPDATE sites SET cert_source='manual',force_https=1,hsts_max_age=300 WHERE id=?", site.ID); err != nil {
								return err
							}
							_, err := tx.ExecContext(ctx, "UPDATE domains SET cert_state='issued' WHERE site_id=?", site.ID)
							return err
						})
					}
					if e != nil {
						t.Fatal(e)
					}
					before, err := st.SiteByID(ctx, site.ID)
					if err != nil {
						t.Fatal(err)
					}
					domains, err := st.ListDomains(ctx, site.ID)
					if err != nil {
						t.Fatal(err)
					}
					redirects, err := st.ListRedirects(ctx, site.ID)
					if err != nil {
						t.Fatal(err)
					}
					config, err := os.ReadFile(filepath.Join(root, "nginx", "sites-available", "demo.conf"))
					if err != nil {
						t.Fatal(err)
					}
					firstProvisioned := provisioned
					r := call(`{"name":"demo","type":"static","primary_domain":"demo.test"}`)
					if !r.OK {
						t.Fatal(r)
					}
					j := wait(r.Data.(jobs.EnqueueResult).Job.ID)
					after, err := st.SiteByID(ctx, site.ID)
					if err != nil {
						t.Fatal(err)
					}
					if j.State != "done" || string(j.Result) != `{"site_id":"`+site.ID+`"}` || !reflect.DeepEqual(before, after) {
						t.Fatalf("active create retry changed desired state: before=%+v after=%+v job=%+v", before, after, j)
					}
					afterDomains, err := st.ListDomains(ctx, site.ID)
					if err != nil {
						t.Fatal(err)
					}
					afterRedirects, err := st.ListRedirects(ctx, site.ID)
					if err != nil {
						t.Fatal(err)
					}
					afterConfig, err := os.ReadFile(filepath.Join(root, "nginx", "sites-available", "demo.conf"))
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(domains, afterDomains) || !reflect.DeepEqual(redirects, afterRedirects) || string(config) != string(afterConfig) || commands.calls != first || provisioned != firstProvisioned {
						t.Fatal("active create retry changed resources")
					}
				})
			}
		})
	}
}

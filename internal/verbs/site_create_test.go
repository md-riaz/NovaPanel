package verbs_test

import (
	"context"
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
		})
	}
}

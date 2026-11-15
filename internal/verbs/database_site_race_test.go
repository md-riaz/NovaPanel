package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

// Both real verbs are admitted before the worker starts, forcing both execution
// orders through the worker's shared site lock and execution-time checks.
func TestSiteDeleteDatabaseCreateOrders(t *testing.T) {
	for _, createFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "create_then_delete", false: "delete_then_create"}[createFirst], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			s, err := store.Open(filepath.Join(root, "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			l, err := audit.Open(s, filepath.Join(root, "audit"))
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			seal, err := secret.New(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			a := agent.New()
			a.Store = s
			a.Audit = l
			a.Secret = seal
			a.Jobs = jobs.NewQueue(s, seal, l)
			a.Cfg.SitesRoot = filepath.Join(root, "sites")
			a.Cfg.TLSDir = filepath.Join(root, "tls")
			for _, d := range []string{"sites-available", "sites-enabled"} {
				if err = os.MkdirAll(filepath.Join(root, "nginx", d), 0755); err != nil {
					t.Fatal(err)
				}
			}
			a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: &siteRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
			if os.Getenv("NOVAPANEL_MARIADB_TEST") == "1" {
				a.Exec = exec.Real{}
			} else {
				a.Exec = &databaseRunner{}
			}
			sid, _ := id.New()
			did, _ := id.New()
			_, err = s.CreateSite(ctx, store.Site{ID: sid, Name: "race", Type: "static", Docroot: filepath.Join(a.Cfg.SitesRoot, "race", "public")}, store.Domain{ID: did, FQDN: "race.test"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SetSiteStatus(ctx, sid, "active"); err != nil {
				t.Fatal(err)
			}
			server := rpc.NewServer(a)
			server.ConfigureJobs()
			call := func(verb string) jobs.EnqueueResult {
				t.Helper()
				b, _ := json.Marshal(map[string]string{"site_id": sid})
				r := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: b})
				if !r.OK {
					t.Fatalf("%s admission: %+v", verb, r)
				}
				return r.Data.(jobs.EnqueueResult)
			}
			var create, del jobs.EnqueueResult
			if createFirst {
				create = call("database.create")
				del = call("site.delete")
			} else {
				del = call("site.delete")
				create = call("database.create")
			}
			workerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- a.Jobs.Run(workerCtx) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					t.Errorf("worker: %v", err)
				}
			}()
			wait := func(job string) store.Job {
				t.Helper()
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					j, e := s.GetJob(ctx, job)
					if e != nil {
						t.Fatal(e)
					}
					if j.State == "done" || j.State == "failed" {
						return j
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("worker timed out")
				return store.Job{}
			}
			cj, dj := wait(create.Job.ID), wait(del.Job.ID)
			if createFirst {
				if cj.State != "done" || dj.State != "failed" || dj.Code != "precondition_failed" {
					t.Fatalf("create=%+v delete=%+v", cj, dj)
				}
				dbs, e := s.ListDatabases(ctx)
				if e != nil || len(dbs) != 1 || dbs[0].SiteID == nil || *dbs[0].SiteID != sid {
					t.Fatalf("attached metadata: %+v %v", dbs, e)
				}
			} else {
				if dj.State != "done" || cj.State != "failed" || cj.Code != "not_found" {
					t.Fatalf("delete=%+v create=%+v", dj, cj)
				}
				if _, e := s.SiteByID(ctx, sid); !errors.Is(e, sql.ErrNoRows) {
					t.Fatalf("site remains: %v", e)
				}
				dbs, e := s.ListDatabases(ctx)
				if e != nil || len(dbs) != 0 {
					t.Fatalf("orphan metadata: %+v %v", dbs, e)
				}
			}
		})
	}
}

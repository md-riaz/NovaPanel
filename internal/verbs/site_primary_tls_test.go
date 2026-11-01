package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTLSPrimaryPublicationExecutor(t *testing.T) {
	for _, failure := range []string{"success", "-t", "reload", "probe", "cancel", "database", "crash", "binding"} {
		for _, targetInstalled := range []bool{false, true} {
			t.Run(failure+map[bool]string{false: "/first", true: "/replacement"}[targetInstalled], func(t *testing.T) {
				ctx := context.Background()
				root := t.TempDir()
				db := filepath.Join(root, "panel.db")
				st, e := store.Open(db)
				if e != nil {
					t.Fatal(e)
				}
				defer func() { st.Close() }()
				log, e := audit.Open(st, filepath.Join(root, "audit.log"))
				if e != nil {
					t.Fatal(e)
				}
				defer log.Close()
				a := agent.New()
				a.Store = st
				a.Audit = log
				a.Cfg.TLSDir = filepath.Join(root, "tls")
				a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
				if e = os.Mkdir(a.Cfg.TLSDir, 0700); e != nil {
					t.Fatal(e)
				}
				ng := filepath.Join(root, "nginx")
				for _, d := range []string{"sites-available", "sites-enabled"} {
					if e = os.MkdirAll(filepath.Join(ng, d), 0755); e != nil {
						t.Fatal(e)
					}
				}
				r := &publicationRunner{}
				a.Nginx = &nginx.Applier{Root: ng, Runner: r, Probe: func(context.Context, nginx.Desired) error { return nil }}
				sid, _ := id.New()
				oldID, _ := id.New()
				newID, _ := id.New()
				if _, e = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: oldID, FQDN: "demo.test"}); e != nil {
					t.Fatal(e)
				}
				if e = st.SetSiteStatus(ctx, sid, "active"); e != nil {
					t.Fatal(e)
				}
				if _, e = st.AddDomain(ctx, store.Domain{ID: newID, SiteID: sid, FQDN: "alias.test"}); e != nil {
					t.Fatal(e)
				}
				names := []string{"demo.test", "alias.test"}
				cp, kp := uploadNamesPEM(t, 1, names)
				pair, e := certs.Validate([]byte(cp), []byte(kp), names, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = certs.Install(a.Cfg.TLSDir, "demo.test", pair); e != nil {
					t.Fatal(e)
				}
				if targetInstalled {
					cp2, kp2 := uploadNamesPEM(t, 2, names)
					other, e := certs.Validate([]byte(cp2), []byte(kp2), names, time.Now())
					if e != nil {
						t.Fatal(e)
					}
					if _, e = certs.Install(a.Cfg.TLSDir, "alias.test", other); e != nil {
						t.Fatal(e)
					}
				}
				if e = st.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.ExecContext(ctx, "UPDATE sites SET cert_source='manual' WHERE id=?", sid)
					return e
				}); e != nil {
					t.Fatal(e)
				}
				server := rpc.NewServer(a)
				server.ConfigureJobs()
				enqueue := func(args any) jobs.Job {
					t.Helper()
					b, _ := json.Marshal(args)
					res := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: b})
					if !res.OK {
						t.Fatalf("admission: %+v", res)
					}
					j, e := st.StartJob(ctx, res.Data.(jobs.EnqueueResult).Job.ID, a.Now())
					if e != nil {
						t.Fatal(e)
					}
					return j
				}
				init := enqueue(map[string]any{"site_id": sid, "maintenance": false})
				if e = server.ExecuteJob(ctx, &init); e != nil {
					t.Fatal(e)
				}
				oldConfig, _ := os.ReadFile(filepath.Join(ng, "sites-available/demo.conf"))
				oldPointer, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "demo.test/current"))
				targetPointer, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "alias.test/current"))
				j := enqueue(map[string]any{"site_id": sid, "primary_domain_id": newID, "maintenance": true})
				runCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				r.fail = failure
				a.Nginx.Probe = func(context.Context, nginx.Desired) error {
					switch failure {
					case "probe":
						return errors.New("probe")
					case "cancel":
						cancel()
						return runCtx.Err()
					case "crash", "binding":
						panic("restart")
					case "database":
						return st.Tx(ctx, func(tx *sql.Tx) error {
							_, e := tx.ExecContext(ctx, "CREATE TRIGGER refuse_primary BEFORE UPDATE OF is_primary ON domains BEGIN SELECT RAISE(ABORT,'injected'); END")
							return e
						})
					}
					return nil
				}
				e = server.ExecuteJob(runCtx, &j)
				if failure == "success" {
					if e != nil {
						t.Fatal(e)
					}
				} else if e == nil {
					t.Fatal("failure ignored")
				}
				p, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "demo.test/current"))
				if p != oldPointer {
					t.Fatal("old path changed")
				}
				if failure != "success" && failure != "crash" && failure != "binding" {
					d, _ := st.DomainByID(ctx, oldID)
					if !d.IsPrimary {
						t.Fatal("primary committed after failure")
					}
					b, _ := os.ReadFile(filepath.Join(ng, "sites-available/demo.conf"))
					if string(b) != string(oldConfig) {
						t.Fatal("config not restored")
					}
					p, _ = os.Readlink(filepath.Join(a.Cfg.TLSDir, "alias.test/current"))
					if p != targetPointer {
						t.Fatal("target pointer not restored")
					}
				}
				intent, _ := st.TLSIntent(ctx)
				if strings.Contains(string(intent), "PRIVATE KEY") || strings.Contains(string(intent), "CERTIFICATE") {
					t.Fatal("secret journal")
				}
				if failure == "crash" || failure == "binding" {
					maintenance := false
					if err := st.UpdateSite(ctx, sid, store.SitePatch{Maintenance: &maintenance}); err == nil {
						t.Fatal("store update bypassed intent owner")
					}
					if len(intent) == 0 {
						t.Fatal("lost recovery intent")
					}
					b, _ := json.Marshal(map[string]any{"site_id": sid, "maintenance": false})
					res := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: b})
					if res.OK {
						t.Fatal("competing update admitted")
					}
				}
				if failure == "database" {
					if e = st.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.ExecContext(ctx, "DROP TRIGGER refuse_primary"); return e }); e != nil {
						t.Fatal(e)
					}
				}
				if e = st.Close(); e != nil {
					t.Fatal(e)
				}
				st, e = store.Open(db)
				if e != nil {
					t.Fatal(e)
				}
				a.Store = st
				log.Close()
				log, e = audit.Open(st, filepath.Join(root, "audit.log"))
				if e != nil {
					t.Fatal(e)
				}
				defer log.Close()
				a.Audit = log
				server = rpc.NewServer(a)
				server.ConfigureJobs()
				if e = st.RecoverJobs(ctx, a.Now()); e != nil {
					t.Fatal(e)
				}
				j, e = st.StartJob(ctx, j.ID, a.Now())
				if e != nil {
					t.Fatal(e)
				}
				before := r.calls
				if failure == "binding" {
					configured := a.Cfg.TLSDir
					a.Cfg.TLSDir = filepath.Join(root, "wrong-root")
					if e = server.ExecuteJob(ctx, &j); e == nil {
						t.Fatal("recovery accepted different root")
					}
					if r.calls != before {
						t.Fatal("binding refusal reloaded")
					}
					a.Cfg.TLSDir = configured
				}
				r.fail = ""
				a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
				if e = server.ExecuteJob(ctx, &j); e != nil {
					t.Fatal(e)
				}
				if failure == "success" && r.calls != before {
					t.Fatal("committed replay reloaded")
				}
				ds, e := st.ListDomains(ctx, sid)
				if e != nil {
					t.Fatal(e)
				}
				count := 0
				for _, d := range ds {
					if d.IsPrimary {
						count++
						if d.ID != newID {
							t.Fatal("wrong primary")
						}
					}
				}
				if count != 1 {
					t.Fatal("not exactly one primary")
				}
				newPair, e := certs.Load(a.Cfg.TLSDir, "alias.test", names, time.Now())
				installedPEM, readErr := os.ReadFile(filepath.Join(a.Cfg.TLSDir, "alias.test/current/fullchain.pem"))
				if readErr != nil || string(installedPEM) != cp {
					t.Fatal("promotion changed certificate bytes")
				}
				if e != nil || newPair.NotAfter != pair.NotAfter {
					t.Fatalf("promoted pair: %v", e)
				}
				b, _ := os.ReadFile(filepath.Join(ng, "sites-available/demo.conf"))
				if !strings.Contains(string(b), "alias.test/current/") || !strings.Contains(string(b), "return 503") {
					t.Fatalf("wrong config: %s", b)
				}
				intent, e = st.TLSIntent(ctx)
				if e != nil || len(intent) != 0 {
					t.Fatalf("intent retained: %s %v", intent, e)
				}
				before = r.calls
				noop := enqueue(map[string]any{"site_id": sid, "primary_domain_id": newID})
				if e = server.ExecuteJob(ctx, &noop); e != nil {
					t.Fatal(e)
				}
				if r.calls != before {
					t.Fatal("same primary reloaded")
				}
				// Exercise terminal publication through the real queue as well as ExecuteJob.
				if e = st.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.ExecContext(ctx, "UPDATE jobs SET state='done' WHERE state IN ('running','queued')")
					return e
				}); e != nil {
					t.Fatal(e)
				}
				raw, _ := json.Marshal(map[string]any{"site_id": sid, "primary_domain_id": oldID})
				reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: raw})
				if !reply.OK {
					t.Fatal(reply)
				}
				queued := reply.Data.(jobs.EnqueueResult).Job
				if failure == "-t" {
					r.fail = "-t"
				}
				workerCtx, stop := context.WithCancel(ctx)
				done := make(chan error, 1)
				go func() { done <- a.Jobs.Run(workerCtx) }()
				deadline := time.Now().Add(5 * time.Second)
				var terminal jobs.Job
				for {
					terminal, e = st.GetJob(ctx, queued.ID)
					if e != nil {
						stop()
						<-done
						t.Fatal(e)
					}
					if terminal.State == "done" || terminal.State == "failed" {
						break
					}
					if time.Now().After(deadline) {
						stop()
						<-done
						t.Fatal("worker timeout")
					}
					time.Sleep(time.Millisecond)
				}
				stop()
				if e = <-done; e != nil {
					t.Fatal(e)
				}
				want := "done"
				if failure == "-t" {
					want = "failed"
				}
				if terminal.State != want {
					t.Fatalf("worker state: %+v", terminal)
				}
				if strings.Contains(string(terminal.Args), "PRIVATE KEY") || strings.Contains(string(terminal.Result), "PRIVATE KEY") {
					t.Fatal("secret terminal")
				}

			})
		}
	}
}

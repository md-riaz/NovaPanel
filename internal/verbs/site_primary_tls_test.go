package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

func TestTLSPrimaryPublicationExecutor(t *testing.T) {
	for _, failure := range []string{"success", "-t", "reload", "probe", "cancel", "database", "crash", "binding"} {
		for _, replacement := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/replacement-%v", failure, replacement), func(t *testing.T) {
				root := t.TempDir()
				f := newPrimaryRestart(t, root, replacement)
				defer f.close(t)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				injected := false
				if failure == "-t" || failure == "reload" {
					f.runner.fail = failure
				}
				f.a.Nginx.Probe = func(context.Context, nginx.Desired) error {
					switch failure {
					case "probe":
						if !injected {
							injected = true
							return errors.New("injected probe failure")
						}
					case "cancel":
						if !injected {
							injected = true
							cancel()
							return context.Canceled
						}
					case "crash", "binding":
						if !injected {
							injected = true
							cancel()
							panic("injected interruption")
						}
					case "database":
						if !injected {
							injected = true
							return f.st.Tx(context.Background(), func(tx *sql.Tx) error {
								_, e := tx.Exec(`CREATE TRIGGER refuse_primary BEFORE UPDATE OF is_primary ON domains BEGIN SELECT RAISE(ABORT,'injected'); END`)
								return e
							})
						}
					}
					return nil
				}
				done := make(chan error, 1)
				go func() { done <- f.a.Jobs.Run(ctx) }()
				deadline := time.Now().Add(4 * time.Second)
				for {
					j, e := f.st.GetJob(context.Background(), f.job)
					if errors.Is(e, store.ErrPublicationPending) {
						time.Sleep(5 * time.Millisecond)
						continue
					}
					if e != nil {
						cancel()
						<-done
						t.Fatal(e)
					}
					if j.State == "done" || j.State == "failed" || ctx.Err() != nil {
						break
					}
					if failure == "database" && j.Attempts >= 2 {
						if e = f.st.Tx(context.Background(), func(tx *sql.Tx) error { _, e := tx.Exec(`DROP TRIGGER IF EXISTS refuse_primary`); return e }); e != nil {
							cancel()
							<-done
							t.Fatal(e)
						}
					}
					if time.Now().After(deadline) {
						cancel()
						<-done
						t.Fatalf("worker timed out: %+v", j)
					}
					time.Sleep(5 * time.Millisecond)
				}
				cancel()
				if e := <-done; e != nil && !errors.Is(e, jobs.ErrRecoveryBlocked) {
					t.Fatal(e)
				}
				if failure == "database" {
					if e := f.st.Tx(context.Background(), func(tx *sql.Tx) error { _, e := tx.Exec(`DROP TRIGGER IF EXISTS refuse_primary`); return e }); e != nil {
						t.Fatal(e)
					}
				}
				pending, err := f.st.TLSIntent(context.Background())
				if err != nil || strings.Contains(string(pending), "PRIVATE KEY") || strings.Contains(string(pending), "CERTIFICATE") {
					t.Fatalf("invalid intent: %s %v", pending, err)
				}
				if failure != "success" && len(pending) == 0 {
					job, e := f.st.GetJob(context.Background(), f.job)
					if e == nil && job.State != "done" {
						old, _ := os.Readlink(filepath.Join(f.a.Cfg.TLSDir, "demo.test/current"))
						target, _ := os.Readlink(filepath.Join(f.a.Cfg.TLSDir, "alias.test/current"))
						config, _ := os.ReadFile(filepath.Join(f.a.Nginx.Root, "sites-available/demo.conf"))
						if old != f.oldPointer || target != f.targetPointer || string(config) != string(f.oldConfig) {
							t.Fatal("failed publication did not restore previous state")
						}
					}
				}
				if failure == "crash" || failure == "binding" || failure == "cancel" {
					if failure != "cancel" {
						maintenance := false
						if err = f.st.UpdateSite(context.Background(), f.site, store.SitePatch{Maintenance: &maintenance}); err == nil {
							t.Fatal("store mutation bypassed unresolved intent")
						}
						raw, _ := json.Marshal(map[string]any{"site_id": f.site, "maintenance": false})
						reply := f.server.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: raw})
						if reply.OK {
							t.Fatal("competing mutation admitted during recovery")
						}
					}
					if failure != "cancel" && len(pending) == 0 {
						t.Fatal("interruption lost intent")
					}
					f.close(t)
					f.reopen(t, root)
					if failure == "binding" {
						original := f.a.Cfg.TLSDir
						f.a.Cfg.TLSDir = filepath.Join(root, "wrong-root")
						before, _ := os.Readlink(filepath.Join(original, "alias.test/current"))
						bad, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
						beforeCalls := f.runner.calls
						recoveryErr := f.a.Jobs.Run(bad)
						if recoveryErr == nil && bad.Err() == nil {
							stop()
							t.Fatal("changed TLS root accepted")
						}
						stop()
						if f.runner.calls != beforeCalls {
							t.Fatal("binding refusal reloaded nginx")
						}
						after, _ := os.Readlink(filepath.Join(original, "alias.test/current"))
						if before != after {
							t.Fatal("binding mismatch published")
						}
						f.a.Cfg.TLSDir = original
						f.server = rpc.NewServer(f.a)
						f.server.ConfigureJobs()
					}
				}
				f.runner.fail = ""
				f.a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
				j, err := f.st.GetJob(context.Background(), f.job)
				if err != nil {
					t.Fatal(err)
				}
				if j.State != "done" {
					runPrimaryQueue(t, f.a, f.job)
				}
				j, err = f.st.GetJob(context.Background(), f.job)
				if err != nil || j.State != "done" || j.Step != "phase4_committed" {
					t.Fatalf("composite job: %+v %v", j, err)
				}
				if (failure == "-t" || failure == "reload" || failure == "probe" || failure == "database") && j.Attempts < 2 {
					t.Fatalf("failure was not retried: %+v", j)
				}
				children, err := f.st.OperationChildren(context.Background(), f.job)
				if err != nil || len(children) != 2 || children[0].Status != "committed" || children[1].Status != "committed" {
					t.Fatalf("children: %+v %v", children, err)
				}
				op, err := f.st.LoadOperation(context.Background(), f.job)
				if err != nil || op.Identity.PublicationAudit == nil || op.Identity.PublicationAudit.State != "acknowledged" {
					t.Fatalf("audit: %+v %v", op, err)
				}
				site, err := f.st.SiteByID(context.Background(), f.site)
				if err != nil || !site.Maintenance {
					t.Fatalf("maintenance: %+v %v", site, err)
				}
				domains, err := f.st.ListDomains(context.Background(), f.site)
				if err != nil {
					t.Fatal(err)
				}
				primaries := 0
				for _, d := range domains {
					if d.IsPrimary {
						primaries++
						if d.ID != f.newID {
							t.Fatal("wrong primary")
						}
					}
				}
				if primaries != 1 {
					t.Fatalf("primary count: %d", primaries)
				}
				pair, err := certs.Load(f.a.Cfg.TLSDir, "alias.test", []string{"demo.test", "alias.test"}, time.Now())
				pem, readErr := os.ReadFile(filepath.Join(f.a.Cfg.TLSDir, "alias.test/current/fullchain.pem"))
				if err != nil || readErr != nil || pair.NotAfter.IsZero() || string(pem) != f.cert {
					t.Fatalf("certificate: %v %v", err, readErr)
				}
				config, err := os.ReadFile(filepath.Join(f.a.Nginx.Root, "sites-available/demo.conf"))
				if err != nil || !strings.Contains(string(config), "alias.test/current/") || !strings.Contains(string(config), "return 503") {
					t.Fatalf("config: %s %v", config, err)
				}
				intent, err := f.st.TLSIntent(context.Background())
				if err != nil || len(intent) != 0 {
					t.Fatalf("intent retained: %s %v", intent, err)
				}
				if strings.Contains(string(j.Args), "PRIVATE KEY") || strings.Contains(string(j.Result), "PRIVATE KEY") {
					t.Fatal("secret in job")
				}
				if err = f.a.PrepareStartup(context.Background()); err != nil {
					t.Fatal(err)
				}
				before := f.runner.calls
				raw, _ := json.Marshal(map[string]any{"site_id": f.site, "primary_domain_id": f.newID})
				reply := f.server.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: raw})
				if !reply.OK {
					t.Fatal(reply)
				}
				runPrimaryQueue(t, f.a, reply.Data.(jobs.EnqueueResult).Job.ID)
				if f.runner.calls != before {
					t.Fatal("same primary reloaded")
				}
				raw, _ = json.Marshal(map[string]any{"site_id": f.site, "primary_domain_id": f.oldID})
				reply = f.server.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: raw})
				if !reply.OK {
					t.Fatal(reply)
				}
				if failure == "-t" {
					f.runner.fail = "-t"
				}
				reverseID := reply.Data.(jobs.EnqueueResult).Job.ID
				runPrimaryQueue(t, f.a, reverseID)
				reverse, err := f.st.GetJob(context.Background(), reverseID)
				want := "done"
				if failure == "-t" {
					want = "failed"
				}
				if err != nil || reverse.State != want || strings.Contains(string(reverse.Args), "PRIVATE KEY") || strings.Contains(string(reverse.Result), "PRIVATE KEY") {
					t.Fatalf("reverse publication: %+v %v", reverse, err)
				}
			})
		}
	}
}

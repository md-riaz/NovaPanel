package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	execboundary "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type primaryRestart struct {
	a                                                        *agent.Agent
	server                                                   *rpc.Server
	st                                                       *store.Store
	log                                                      *audit.Log
	runner                                                   *publicationRunner
	job, site, oldID, newID, oldPointer, targetPointer, cert string
	oldConfig                                                []byte
}

func newPrimaryRestart(t *testing.T, rootDir string, targetInstalled bool) *primaryRestart {
	t.Helper()
	ctx := context.Background()
	root := rootDir
	db := filepath.Join(root, "panel.db")
	st, e := store.Open(db)
	if e != nil {
		t.Fatal(e)
	}

	log, e := audit.Open(st, filepath.Join(root, "audit.log"))
	if e != nil {
		t.Fatal(e)
	}

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
		return res.Data.(jobs.EnqueueResult).Job
	}
	init := enqueue(map[string]any{"site_id": sid, "maintenance": false})
	runPrimaryQueue(t, a, init.ID)
	oldConfig, _ := os.ReadFile(filepath.Join(ng, "sites-available/demo.conf"))
	oldPointer, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "demo.test/current"))
	targetPointer, _ := os.Readlink(filepath.Join(a.Cfg.TLSDir, "alias.test/current"))
	j := enqueue(map[string]any{"site_id": sid, "primary_domain_id": newID, "maintenance": true})
	return &primaryRestart{a: a, server: server, st: st, log: log, runner: r, job: j.ID, site: sid, oldID: oldID, newID: newID, oldPointer: oldPointer, targetPointer: targetPointer, oldConfig: oldConfig, cert: cp}
}

func (f *primaryRestart) close(t *testing.T) {
	t.Helper()
	if err := f.log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
}
func (f *primaryRestart) reopen(t *testing.T, root string) {
	t.Helper()
	var err error
	f.st, err = store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.log, err = audit.Open(f.st, filepath.Join(root, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	f.a.Store = f.st
	f.a.Audit = f.log
	f.server = rpc.NewServer(f.a)
	f.server.ConfigureJobs()
}
func runPrimaryQueue(t *testing.T, a *agent.Agent, jid string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for {
		j, err := a.Store.GetJob(ctx, jid)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "done" || j.State == "failed" {
			return
		}
		if ctx.Err() != nil {
			t.Fatal("queue timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
func (f *primaryRestart) check(t *testing.T, success bool) {
	t.Helper()
	ctx := context.Background()
	j, err := f.st.GetJob(ctx, f.job)
	if err != nil {
		t.Fatal(err)
	}
	if success && (j.State != "done" || j.Step != "tls_committed") {
		t.Fatalf("original job not completed: %+v", j)
	}
	if !success && j.State != "failed" {
		t.Fatalf("original job not failed: %+v", j)
	}
	ds, err := f.st.ListDomains(ctx, f.site)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, d := range ds {
		if d.IsPrimary {
			count++
			if (success && d.ID != f.newID) || (!success && d.ID != f.oldID) {
				t.Fatal("wrong primary")
			}
		}
	}
	if count != 1 {
		t.Fatal("not exactly one primary")
	}
	old, _ := os.Readlink(filepath.Join(f.a.Cfg.TLSDir, "demo.test/current"))
	if old != f.oldPointer {
		t.Fatal("old pointer changed")
	}
	config, err := os.ReadFile(filepath.Join(f.a.Nginx.Root, "sites-available/demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if success {
		cert, err := os.ReadFile(filepath.Join(f.a.Cfg.TLSDir, "alias.test/current/fullchain.pem"))
		if err != nil || string(cert) != f.cert {
			t.Fatal("certificate changed")
		}
		if !strings.Contains(string(config), "alias.test/current/") || !strings.Contains(string(config), "return 503") {
			t.Fatal("wrong promoted config")
		}
	} else {
		target, _ := os.Readlink(filepath.Join(f.a.Cfg.TLSDir, "alias.test/current"))
		if target != f.targetPointer || string(config) != string(f.oldConfig) {
			t.Fatal("rollback did not restore config and target")
		}
	}
	raw, err := f.st.TLSIntent(ctx)
	if err != nil || len(raw) != 0 {
		t.Fatalf("journal retained: %s %v", raw, err)
	}
}
func TestPrimaryTLSActualQueueStartup(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		for _, mode := range []string{"success", "probe", "cancel", "metadata"} {
			t.Run(fmt.Sprintf("replacement-%v/%s", replacement, mode), func(t *testing.T) {
				root := t.TempDir()
				f := newPrimaryRestart(t, root, replacement)
				f.a.Nginx.Probe = func(context.Context, nginx.Desired) error { panic("interruption") }
				runPrimaryQueue(t, f.a, f.job)
				j, _ := f.st.GetJob(context.Background(), f.job)
				if j.Code != "interrupted" {
					t.Fatalf("not interrupted: %+v", j)
				}
				f.close(t)
				f.reopen(t, root)
				defer f.close(t)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				f.a.Nginx.Probe = func(context.Context, nginx.Desired) error {
					switch mode {
					case "probe":
						return errors.New("probe failure")
					case "cancel":
						cancel()
						return context.Canceled
					case "metadata":
						return f.st.Tx(context.Background(), func(tx *sql.Tx) error {
							_, err := tx.Exec("CREATE TRIGGER refuse_primary BEFORE UPDATE OF is_primary ON domains BEGIN SELECT RAISE(ABORT,'injected'); END")
							return err
						})
					}
					return nil
				}
				done := make(chan error, 1)
				go func() { done <- f.a.Jobs.Run(ctx) }()
				deadline := time.Now().Add(5 * time.Second)
				for {
					j, _ = f.st.GetJob(context.Background(), f.job)
					if j.State == "done" || (j.State == "failed" && j.Code != "interrupted") {
						break
					}
					select {
					case err := <-done:
						t.Fatalf("startup stopped: %v", err)
					default:
					}
					if time.Now().After(deadline) {
						cancel()
						<-done
						t.Fatalf("restart timeout: %+v", j)
					}
					time.Sleep(time.Millisecond)
				}
				cancel()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				f.check(t, mode == "success")
			})
		}
	}
}
func TestPrimaryTLSKillHelper(t *testing.T) {
	root := os.Getenv("NOVAPANEL_PRIMARY_KILL_ROOT")
	if root == "" {
		t.Skip("owned child helper")
	}
	replacement := os.Getenv("NOVAPANEL_PRIMARY_REPLACEMENT") == "1"
	f := newPrimaryRestart(t, root, replacement)
	f.a.Nginx.Probe = func(ctx context.Context, _ nginx.Desired) error {
		if err := os.WriteFile(filepath.Join(root, "ready"), []byte(fmt.Sprintf("%d %s", os.Getpid(), f.job)), 0600); err != nil {
			panic(err)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	if err := f.a.Jobs.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestPrimaryTLSSIGKILLQueueStartup(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprint(replacement), func(t *testing.T) {
			root := t.TempDir()
			rep := "0"
			if replacement {
				rep = "1"
			}
			childCtx, kill := context.WithCancel(context.Background())
			defer kill()
			done := make(chan error, 1)
			go func() {
				_, _, _, err := (execboundary.Real{}).Run(childCtx, os.Args[0], []string{"-test.run=^TestPrimaryTLSKillHelper$"}, execboundary.RunOpts{Env: append(os.Environ(), "NOVAPANEL_PRIMARY_KILL_ROOT="+root, "NOVAPANEL_PRIMARY_REPLACEMENT="+rep)})
				done <- err
			}()
			var jid []byte
			deadline := time.Now().Add(10 * time.Second)
			for {
				var err error
				jid, err = os.ReadFile(filepath.Join(root, "ready"))
				if err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child did not reach probe")
				}
				time.Sleep(10 * time.Millisecond)
			}
			fields := strings.Fields(string(jid))
			if len(fields) != 2 {
				t.Fatal("invalid child marker")
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			jid = []byte(fields[1])
			exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if err != nil || exe != os.Args[0] {
				t.Fatal("child executable mismatch")
			}
			status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
			if err != nil || !strings.Contains(string(status), fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d", os.Getuid(), os.Geteuid(), os.Geteuid(), os.Geteuid())) {
				t.Fatal("child credentials mismatch")
			}
			kill()
			if err = <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("owned child not killed: %v", err)
			}
			a := agent.New()
			a.Cfg.TLSDir = filepath.Join(root, "tls")
			a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
			a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: &publicationRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
			f := &primaryRestart{a: a, job: string(jid)}
			f.reopen(t, root)
			defer f.close(t)
			j, err := f.st.GetJob(context.Background(), f.job)
			if err != nil || j.State != "running" {
				t.Fatalf("not original running job: %+v %v", j, err)
			}
			runPrimaryQueue(t, a, f.job)
			j, err = f.st.GetJob(context.Background(), f.job)
			if err != nil || j.State != "done" || j.Step != "tls_committed" {
				t.Fatalf("original job not recovered: %+v %v", j, err)
			}
			var args struct {
				SiteID string `json:"site_id"`
				Target string `json:"primary_domain_id"`
			}
			if err = json.Unmarshal(j.Args, &args); err != nil {
				t.Fatal(err)
			}
			ds, err := f.st.ListDomains(context.Background(), args.SiteID)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range ds {
				if d.IsPrimary && d.ID != args.Target {
					t.Fatal("wrong primary")
				}
			}
			raw, err := f.st.TLSIntent(context.Background())
			if err != nil || len(raw) != 0 {
				t.Fatal("journal not cleared")
			}
			b, err := os.ReadFile(filepath.Join(a.Nginx.Root, "sites-available/demo.conf"))
			if err != nil || !strings.Contains(string(b), "alias.test/current/") || !strings.Contains(string(b), "return 503") {
				t.Fatal("promoted config missing")
			}
			t.Log("recovered original site.update after SIGKILL at injected post-reload/pre-metadata probe")
		})
	}
}

func TestPrimaryTLSStartupRejectsChangedBinding(t *testing.T) {
	for _, mode := range []string{"role", "effect", "terminal", "args", "no-primary", "hsts-authority", "patch", "domains", "site", "target", "old-root", "old-version", "new-root", "new-primary", "stale-version", "equal-version", "version-one", "version-unknown", "trailing", "corrupt", "wrong-job", "nginx-root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			f := newPrimaryRestart(t, root, true)
			f.a.Nginx.Probe = func(context.Context, nginx.Desired) error { panic("interrupt") }
			runPrimaryQueue(t, f.a, f.job)
			ctx := context.Background()
			original, err := f.st.TLSIntent(ctx)
			if err != nil || len(original) == 0 {
				t.Fatal("missing intent")
			}
			var in map[string]any
			if err = json.Unmarshal(original, &in); err != nil {
				t.Fatal(err)
			}
			promotion := in["promotion"].(map[string]any)
			token := in["token"].(map[string]any)
			old := in["old_token"].(map[string]any)
			// Durable authority changes must be refused before any publication or generic update.
			switch mode {
			case "role":
				err = f.st.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.Exec("UPDATE jobs SET role='read_only' WHERE id=?", f.job)
					return e
				})
			case "effect":
				err = f.st.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("UPDATE jobs SET effectful=1 WHERE id=?", f.job); return e })
			case "terminal":
				j, _ := f.st.GetJob(ctx, f.job)
				j.State = "done"
				err = f.st.UpdateJob(ctx, j)
			case "args", "no-primary", "hsts-authority":
				j, _ := f.st.GetJob(ctx, f.job)
				var args map[string]any
				_ = json.Unmarshal(j.Args, &args)
				if mode == "args" {
					args["maintenance"] = false
				}
				if mode == "no-primary" {
					delete(args, "primary_domain_id")
				}
				if mode == "hsts-authority" {
					args["hsts_max_age"] = float64(0)
					j.Role = "admin"
					promotion["patch"].(map[string]any)["HSTSMaxAge"] = float64(0)
				}
				j.Args, _ = json.Marshal(args)
				err = f.st.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.Exec("UPDATE jobs SET args_json=?,role=? WHERE id=?", string(j.Args), j.Role, j.ID)
					return e
				})
			case "patch":
				promotion["patch"].(map[string]any)["Maintenance"] = false
			case "domains":
				in["domains"] = []string{"untrusted.test"}
			case "site":
				promotion["site"].(map[string]any)["Name"] = "untrusted"
			case "target":
				promotion["target"] = f.oldID
			case "old-root":
				old["root"] = filepath.Join(root, "untrusted")
			case "old-version":
				old["version"], _ = id.New()
				old["previous"] = old["version"]
			case "new-root":
				token["root"] = filepath.Join(root, "untrusted")
			case "new-primary":
				token["primary"] = "untrusted.test"
			case "stale-version":
				token["version"], _ = id.New()
			case "equal-version":
				token["version"] = token["previous"]
			case "version-one":
				in["version"] = float64(1)
			case "version-unknown":
				in["version"] = float64(99)
			case "wrong-job":
				in["job_id"], _ = id.New()
			case "nginx-root":
				in["nginx_root"] = filepath.Join(root, "untrusted")
			}
			if err != nil {
				t.Fatal(err)
			}
			changed, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "trailing" {
				changed = append(changed, []byte(` {}`)...)
			}
			if mode == "corrupt" {
				changed = []byte(`{"version":`)
			}
			if err = f.st.ClearTLSIntent(ctx, original); err != nil {
				t.Fatal(err)
			}
			if err = f.st.BeginTLSIntent(ctx, changed); err != nil {
				t.Fatal(err)
			}
			f.close(t)
			f.reopen(t, root)
			defer f.close(t)
			calls := 0
			f.a.Nginx.Probe = func(context.Context, nginx.Desired) error { calls++; return nil }
			before, _ := os.ReadFile(filepath.Join(f.a.Nginx.Root, "sites-available/demo.conf"))
			pointer, _ := os.Readlink(filepath.Join(f.a.Cfg.TLSDir, "alias.test/current"))
			run, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err = f.a.Jobs.Run(run)
			if !errors.Is(err, jobs.ErrRecoveryBlocked) {
				t.Fatalf("expected fail closed: %v", err)
			}
			pending, _ := f.st.TLSIntent(ctx)
			if string(pending) != string(changed) || calls != 0 {
				t.Fatal("unresolved journal changed or publication ran")
			}
			after, _ := os.ReadFile(filepath.Join(f.a.Nginx.Root, "sites-available/demo.conf"))
			afterPointer, _ := os.Readlink(filepath.Join(f.a.Cfg.TLSDir, "alias.test/current"))
			if string(before) != string(after) || pointer != afterPointer {
				t.Fatal("rejected recovery changed files")
			}
			read := f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.list", Args: json.RawMessage(`{}`)})
			if !read.OK {
				t.Fatalf("reads unavailable: %+v", read)
			}
			raw, _ := json.Marshal(map[string]any{"site_id": f.site, "maintenance": false})
			mutation := f.server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.update", Args: raw})
			if mutation.OK {
				t.Fatal("mutation passed unresolved journal")
			}
		})
	}
}

func TestPrimaryTLSCommittedStartupFinishesOriginalJob(t *testing.T) {
	root := t.TempDir()
	f := newPrimaryRestart(t, root, false)
	runPrimaryQueue(t, f.a, f.job)
	ctx := context.Background()
	j, err := f.st.GetJob(ctx, f.job)
	if err != nil || j.Step != "tls_committed" {
		t.Fatal("publication not committed")
	}
	j.State = "running"
	j.Finished = nil
	if err = f.st.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	f.close(t)
	f.reopen(t, root)
	defer f.close(t)
	f.a.Nginx.Probe = func(context.Context, nginx.Desired) error {
		t.Error("committed publication repeated")
		return errors.New("must not publish")
	}
	f.a.Jobs.SetRunner(func(context.Context, *jobs.Job) error {
		t.Error("general executor called during recovery")
		return errors.New("must not execute")
	})
	runPrimaryQueue(t, f.a, f.job)
	f.check(t, true)
}

package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

type acceptanceClock struct{ now int64 }

func (c *acceptanceClock) Now() int64 { return c.now }

// The only provisioning substitution is scratch directory creation. No host user
// operations are permitted in this nonroot lab; nginx, queue and ACME are real.
func TestRealPebbleSchedulerMutationAcceptance(t *testing.T) {
	if os.Getenv("NOVAPANEL_PEBBLE") != "1" {
		t.Skip("confined Pebble lab only")
	}
	l, ap := newNginxLab(t)
	ctx := context.Background()
	var st *store.Store
	var log *audit.Log
	var a *agent.Agent
	var server *rpc.Server
	var cancel context.CancelFunc
	var done chan error
	clock := &acceptanceClock{time.Now().UnixMilli()}
	open := func() {
		var err error
		st, err = store.Open(l.root + "/panel.db")
		if err != nil {
			t.Fatal(err)
		}
		log, err = audit.Open(st, l.root+"/audit.log")
		if err != nil {
			t.Fatal(err)
		}
		a = agent.New()
		a.Store = st
		a.Audit = log
		a.Nginx = ap
		a.Secret, err = secret.New(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		a.Cfg.TLSDir = l.root + "/tls"
		a.Cfg.SitesRoot = l.root + "/sites"
		a.Cfg.ACMEWebroot = l.root + "/webroot"
		a.Cfg.ACMEDirectoryStaging = "https://localhost:14000/dir"
		a.Cfg.ACMEUseStaging = true
		// A local Pebble endpoint in the production config slot exercises provenance
		// and force-HTTPS policy, not public CA trust or production issuance.
		a.Cfg.ACMEDirectoryProd = "https://localhost:14000/dir"
		a.Cfg.ACMEEmail = "lab@example.test"
		a.ProvisionSite = func(_ context.Context, name string) error {
			if err := os.MkdirAll(filepath.Join(a.Cfg.ACMEWebroot, name), 0755); err != nil {
				return err
			}
			path := filepath.Join(a.Cfg.SitesRoot, name, "public")
			if err := os.MkdirAll(path, 0700); err != nil {
				return err
			}
			return os.WriteFile(path+"/index.html", []byte("phase3-real-nginx"), 0600)
		}
		server = rpc.NewServer(a)
		server.ConfigureJobs()
		a.Scheduler = jobs.NewScheduler(a.Jobs, clock, nil)
	}
	start := func() {
		var c context.Context
		c, cancel = context.WithCancel(ctx)
		done = make(chan error, 1)
		go func() { done <- a.Jobs.Run(c) }()
	}
	stop := func() {
		if cancel != nil {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
			cancel = nil
		}
	}
	open()
	start()
	defer func() { stop(); log.Close(); st.Close() }()
	dispatch := func(verb string, args any) rpc.Reply {
		raw, _ := json.Marshal(args)
		return server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: raw, Actor: "untrusted-label"})
	}
	wait := func(j jobs.Job) jobs.Job {
		t.Helper()
		until := time.Now().Add(90 * time.Second)
		for time.Now().Before(until) {
			got, err := st.GetJob(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State == "failed" {
				t.Fatalf("%s failed code=%s", got.Verb, got.Code)
			}
			if got.State == "done" {
				return got
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timeout %s", j.Verb)
		return j
	}
	all := []jobs.Job{}
	enqueue := func(verb string, args any, key string) jobs.Job {
		t.Helper()
		r := dispatch(verb, args)
		if !r.OK {
			t.Fatalf("%s admission: %+v", verb, r)
		}
		en, ok := r.Data.(jobs.EnqueueResult)
		if !ok {
			t.Fatalf("%s not queued", verb)
		}
		if en.Job.ObjectKey != key || en.Job.CreatedBy != "local-root" || en.Job.Role != "owner" {
			t.Fatalf("binding %+v", en.Job)
		}
		all = append(all, en.Job)
		return en.Job
	}
	run := func(verb string, args any, key string) jobs.Job { t.Helper(); return wait(enqueue(verb, args, key)) }
	j := run("site.create", map[string]any{"name": "demo", "type": "static", "primary_domain": "demo.test"}, "site:demo")
	var created struct {
		SiteID string `json:"site_id"`
	}
	if err := json.Unmarshal(j.Result, &created); err != nil || created.SiteID == "" {
		t.Fatal("create result", err)
	}
	sid := created.SiteID
	key := "site:" + sid
	args := map[string]any{"site_id": sid}
	run("site.update", map[string]any{"site_id": sid, "maintenance": false}, key)
	run("ssl.issue", args, key)
	first, err := os.Readlink(a.Cfg.TLSDir + "/demo.test/current")
	if err != nil {
		t.Fatal(err)
	}
	// Exact 30-day boundary uses persisted metadata and an injected scheduler clock;
	// agent/certificate validation retains real wall time for Pebble's short certs.
	expiry := func(days int) {
		t.Helper()
		if err := st.Tx(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, "UPDATE domains SET cert_not_after=? WHERE site_id=?", clock.now+int64(days)*24*60*60*1000, sid)
			return e
		}); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() {
		t.Helper()
		if err := a.Scheduler.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		rows, e := st.ListJobs(ctx, store.JobFilter{})
		if e != nil {
			t.Fatal(e)
		}
		return len(rows)
	}
	expiry(31)
	n := count()
	tick()
	if count() != n {
		t.Fatal("renewed outside 30-day window")
	}
	expiry(30)
	tick()
	rows, err := st.ListJobs(ctx, store.JobFilter{Verb: "ssl.renew"})
	if err != nil || len(rows) != 1 {
		t.Fatal("scheduled renewal", rows, err)
	}
	scheduled := rows[0]
	if scheduled.Actor != "scheduler@local" || scheduled.Role != "owner" || scheduled.CreatedBy != "local-root" || !strings.Contains(string(scheduled.Args), `"staging":true`) {
		t.Fatalf("schedule provenance %+v", scheduled)
	}
	all = append(all, scheduled)
	before := l.reloads
	wait(scheduled)
	second, err := os.Readlink(a.Cfg.TLSDir + "/demo.test/current")
	if err != nil || second == first || l.reloads != before+1 {
		t.Fatal("scheduled renewal must swap and reload exactly once", err)
	}
	cadence, err := st.GetSchedule(ctx, "acme:auto:"+sid)
	if err != nil || cadence.Cron != "daily" || cadence.NextRun <= clock.now {
		t.Fatal("daily cadence", cadence, err)
	}
	// Full store/audit/server/queue restart, not just scheduler reconstruction.
	stop()
	if err = log.Close(); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	open()
	start()
	n = count()
	before = l.reloads
	tick()
	tick()
	if count() != n || l.reloads != before {
		t.Fatal("restart duplicated job or reload")
	}
	after, err := st.GetSchedule(ctx, cadence.ID)
	if err != nil || after.NextRun != cadence.NextRun {
		t.Fatal("cadence lost", err)
	}
	r := dispatch("ssl.issue", args)
	if !r.OK {
		t.Fatal(r)
	}
	if _, ok := r.Data.(jobs.EnqueueResult); ok {
		t.Fatal("duplicate issue queued")
	}
	if l.reloads != before {
		t.Fatal("no-op issue reloaded")
	}
	// Both environment slots point only to loopback Pebble. False is explicit and
	// persisted; it must not silently fall back to the staging default.
	run("ssl.renew", map[string]any{"site_id": sid, "staging": false}, key)
	run("site.update", map[string]any{"site_id": sid, "force_https": true}, key)
	labGet(t, "/", 301, "")
	added := run("domain.add", map[string]any{"site_id": sid, "fqdn": "alias.test"}, key)
	var domain struct {
		ID string `json:"domain_id"`
	}
	json.Unmarshal(added.Result, &domain)
	tick()
	issues, _ := st.ListJobs(ctx, store.JobFilter{Verb: "ssl.issue"})
	var expansion jobs.Job
	for _, v := range issues {
		if v.Actor == "scheduler@local" {
			expansion = v
		}
	}
	if expansion.ID == "" || !strings.Contains(string(expansion.Args), `"staging":false`) {
		t.Fatal("missing full-set expansion", issues)
	}
	all = append(all, expansion)
	wait(expansion)
	pair, err := certs.Load(a.Cfg.TLSDir, "demo.test", []string{"demo.test", "alias.test"}, time.Now())
	if err != nil || len(pair.FQDNs) != 2 {
		t.Fatal("SAN expansion", err)
	}
	ds, err := st.ListDomains(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.CertState != "issued" || d.CertStaging {
			t.Fatal("expanded coverage", d)
		}
	}
	labGet(t, "/.well-known/acme-challenge/missing", 404, "")
	run("site.update", map[string]any{"site_id": sid, "primary_domain_id": domain.ID}, key)
	ds, _ = st.ListDomains(ctx, sid)
	for _, d := range ds {
		if d.ID == domain.ID && !d.IsPrimary {
			t.Fatal("primary not promoted")
		}
	}
	redirects := run("redirect.add", map[string]any{"site_id": sid, "source": "/old", "target": "/new", "code": 302}, key)
	var redirect struct {
		ID string `json:"redirect_id"`
	}
	json.Unmarshal(redirects.Result, &redirect)
	run("redirect.remove", map[string]any{"redirect_id": redirect.ID}, key)
	// Hold the first real executor at the runner boundary while admitting a second
	// mutation concurrently. The second must remain queued until the first returns.
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.Jobs.SetRunner(func(c context.Context, j *jobs.Job) error {
		if j.Verb == "site.update" {
			once.Do(func() { close(entered); <-release })
		}
		return server.ExecuteJob(c, j)
	})
	one := enqueue("site.update", map[string]any{"site_id": sid, "maintenance": true}, key)
	<-entered
	two := enqueue("site.update", map[string]any{"site_id": sid, "maintenance": false}, key)
	time.Sleep(150 * time.Millisecond)
	pending, _ := st.GetJob(ctx, two.ID)
	if pending.State != "queued" {
		t.Fatal("overlapping execution", pending.State)
	}
	close(release)
	wait(one)
	wait(two)
	a.Jobs.SetRunner(server.ExecuteJob)
	// Upload the genuine issued pair as a manual input. This tests sealing and real
	// publication without manufacturing an ACME response or exposing PEM in output.
	cp, err := os.ReadFile(a.Cfg.TLSDir + "/alias.test/current/fullchain.pem")
	if err != nil {
		t.Fatal(err)
	}
	kp, err := os.ReadFile(a.Cfg.TLSDir + "/alias.test/current/privkey.pem")
	if err != nil {
		t.Fatal(err)
	}
	upload := enqueue("ssl.upload", map[string]any{"site_id": sid, "cert_pem": string(cp), "key_pem": string(kp)}, key)
	if bytes.Contains(upload.Args, []byte("-----BEGIN")) {
		t.Fatal("plaintext input")
	}
	wait(upload)
	r = dispatch("ssl.auto_set", map[string]any{"site_id": sid, "enabled": true})
	if !r.OK {
		t.Fatal(r)
	}
	if _, ok := r.Data.(jobs.EnqueueResult); ok {
		t.Fatal("documented synchronous auto_set unexpectedly queued")
	}
	site, err := st.SiteByID(ctx, sid)
	if err != nil || site.CertSource != "manual" || !site.SSLAuto {
		t.Fatal("manual state", err)
	}
	// Advance only the scheduler clock to cross persisted cadence. Manual source
	// must suppress even the pre-existing automatic schedule's next occurrence.
	n = count()
	before = l.reloads
	clock.now = cadence.NextRun
	tick()
	if count() != n || l.reloads != before {
		t.Fatal("manual certificate auto-renewed")
	}
	run("domain.remove", map[string]any{"domain_id": ds[0].ID}, key)
	run("site.delete", map[string]any{"site_id": sid, "purge_files": false}, key)
	if _, err = st.SiteByID(ctx, sid); err != sql.ErrNoRows {
		t.Fatal("delete did not remove site", err)
	}
	// Every queued mutation has exactly one durable enqueue and terminal event.
	events, err := st.ListEvents(ctx, 1000, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range all {
		en, ex := 0, 0
		for _, ev := range events {
			var d map[string]any
			json.Unmarshal(ev.Detail, &d)
			if d["job_id"] != job.ID {
				continue
			}
			if ev.Verb != job.Verb || ev.ObjectKey != job.ObjectKey || ev.Role != job.Role || ev.Actor != job.Actor || ev.Outcome != "ok" {
				t.Fatalf("audit mismatch %+v", ev)
			}
			if d["stage"] == "enqueue" {
				en++
			}
			if d["stage"] == "exec" {
				ex++
			}
		}
		if en != 1 || ex != 1 {
			t.Fatalf("%s audit enqueue=%d exec=%d", job.Verb, en, ex)
		}
	}
	for _, path := range []string{"panel.db", "panel.db-wal", "audit.log"} {
		data, e := os.ReadFile(l.root + "/" + path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(data, cp) || bytes.Contains(data, kp) || bytes.Contains(data, []byte("-----BEGIN")) {
			t.Fatal("PEM persisted in", path)
		}
	}
	t.Logf("real queue mutation matrix verified %d jobs; scheduler cadence, restart, SAN expansion, sealed upload and manual exclusion passed", len(all))
}

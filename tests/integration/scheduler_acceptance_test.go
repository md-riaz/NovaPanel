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
	caller := rpc.RootCaller(0)
	dispatch := func(verb string, args any) rpc.Reply {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		before, err := st.ListEvents(ctx, 1000, "")
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, ev := range before {
			ids[ev.ID] = true
		}
		reply := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: verb, Args: raw, Actor: "untrusted-label"})
		if !reply.OK {
			t.Fatalf("%s dispatch failed code=%s", verb, reply.Code)
		}
		var target map[string]any
		if err := json.Unmarshal(raw, &target); err != nil {
			t.Fatal(err)
		}
		// Dispatch records the pre-handler key. Child handlers resolve the
		// parent later; the queue events must use the resolved site ID instead.
		wantKey := ""
		switch verb {
		case "site.create":
			wantKey = "site:" + target["name"].(string)
		case "domain.remove", "redirect.remove":
			wantKey = "site:"
		case "ssl.auto_set":
		default:
			wantKey = "site:" + target["site_id"].(string)
		}
		after, err := st.ListEvents(ctx, 1000, "")
		if err != nil {
			t.Fatal(err)
		}
		stages := map[string]int{}
		for _, ev := range after {
			if ids[ev.ID] {
				continue
			}
			var detail map[string]any
			if err := json.Unmarshal(ev.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			if _, queued := detail["job_id"]; queued {
				continue
			}
			if ev.Verb != verb || ev.ObjectKey != wantKey || ev.Actor != "untrusted-label" || ev.Role != caller.Role || ev.Outcome != "ok" || ev.Code != "" || len(detail) != 1 {
				t.Fatalf("%s dispatch audit binding differs", verb)
			}
			stage, _ := detail["stage"].(string)
			stages[stage]++
		}
		if len(stages) != 2 || stages["authz"] != 1 || stages["exec"] != 1 {
			t.Fatalf("%s dispatch stage counts differ: %v", verb, stages)
		}
		return reply
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
		wantRole, wantOwner := caller.Role, "local-root"
		if caller.Kind == "token" {
			wantRole, wantOwner = "token", caller.Session
		}
		if en.Job.Verb != verb || en.Job.ObjectKey != key || en.Job.CreatedBy != wantOwner || en.Job.Role != wantRole || en.Job.Actor != "untrusted-label" {
			t.Fatalf("job authority/target binding differs for %s", verb)
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
	// A token's incoming role label is not its stored worker authority.
	caller = rpc.Caller{Kind: "token", Role: "owner", Session: "acceptance-token", Scopes: []string{"sites:write"}, Peer: 1234}
	tokenJob := run("site.update", map[string]any{"site_id": sid, "maintenance": false}, key)
	if len(tokenJob.Scopes) != 1 || tokenJob.Scopes[0] != "sites:write" || tokenJob.Automation {
		t.Fatal("token authority snapshot differs")
	}
	caller = rpc.RootCaller(0)
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
	budget := func() string {
		t.Helper()
		raw, e := st.Setting(ctx, "acme:policy:staging:domain:demo.test")
		if e != nil {
			t.Fatal(e)
		}
		return string(raw)
	}
	budgetBefore := budget()
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
	noop := run("ssl.issue", args, key)
	var noopResult struct {
		FQDNs    []string `json:"fqdns"`
		NotAfter int64    `json:"not_after"`
	}
	pairBefore, err := certs.Load(a.Cfg.TLSDir, "demo.test", []string{"demo.test"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(noop.Result, &noopResult); err != nil || len(noopResult.FQDNs) != 1 || noopResult.FQDNs[0] != "demo.test" || noopResult.NotAfter != pairBefore.NotAfter.UnixMilli() {
		t.Fatalf("queued no-op canonical result: %s: %v", noop.Result, err)
	}
	if l.reloads != before {
		t.Fatal("no-op issue reloaded")
	}
	if budget() != budgetBefore {
		t.Fatal("restart/no-op consumed CA rate budget")
	}
	// Both environment slots point only to loopback Pebble. False is explicit and
	// persisted; it must not silently fall back to the staging default.
	run("ssl.issue", map[string]any{"site_id": sid, "staging": false}, key)
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
	r := dispatch("ssl.auto_set", map[string]any{"site_id": sid, "enabled": false})
	if !r.OK {
		t.Fatal(r)
	}
	n = count()
	before = l.reloads
	clock.now = cadence.NextRun
	tick()
	if count() != n || l.reloads != before {
		t.Fatal("disabled automatic certificate renewed")
	}
	r = dispatch("ssl.auto_set", map[string]any{"site_id": sid, "enabled": true})
	if !r.OK {
		t.Fatal(r)
	}
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
	clock.now = cadence.NextRun + 24*60*60*1000
	tick()
	if count() != n || l.reloads != before {
		t.Fatal("manual certificate auto-renewed")
	}
	// A real nginx validation failure must terminate through Queue.Run and audit
	// error without a reload, rather than bypassing the executor with a fake error.
	l.invalid = true
	failed := enqueue("site.update", map[string]any{"site_id": sid, "maintenance": true}, key)
	all = all[:len(all)-1]
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		got, e := st.GetJob(ctx, failed.ID)
		if e != nil {
			t.Fatal(e)
		}
		if got.State == "failed" {
			failed = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if failed.State != "failed" || failed.Code != "agent_error" {
		t.Fatal("missing executor failure", failed.State, failed.Code)
	}
	run("domain.remove", map[string]any{"domain_id": ds[0].ID}, key)
	run("site.delete", map[string]any{"site_id": sid, "purge_files": false}, key)
	if _, err = st.SiteByID(ctx, sid); err != sql.ErrNoRows {
		t.Fatal("delete did not remove site", err)
	}
	// Refusals precede typed decoding/target lookup, so object_key is exactly
	// empty, even for well-formed target arguments. Do not invent a resolved key.
	verbs := []string{"site.create", "site.update", "site.delete", "domain.add", "domain.remove", "redirect.add", "redirect.remove", "ssl.issue", "ssl.renew", "ssl.upload", "ssl.auto_set"}
	refusalCallers := []rpc.Caller{
		{Role: "read_only", Session: "acceptance-reader", Peer: 1234},
		{Kind: "token", Role: "owner", Session: "acceptance-unscoped-token", Peer: 1234},
	}
	beforeRefusals := count()
	for i, incoming := range refusalCallers {
		for _, verb := range verbs {
			raw, err := json.Marshal(map[string]any{"site_id": sid, "key_pem": string(kp)})
			if err != nil {
				t.Fatal(err)
			}
			actor := []string{"owner", "token-owner npat_acceptance_canary"}[i]
			r := server.Dispatch(rpc.WithCaller(ctx, incoming), rpc.Request{Verb: verb, Actor: actor, Args: raw})
			if r.OK || r.Code != "forbidden" {
				t.Fatalf("%s role refusal code=%s", verb, r.Code)
			}
		}
	}
	if count() != beforeRefusals {
		t.Fatal("authorization refusal enqueued a job")
	}
	// Every queued mutation has exactly one durable enqueue and terminal event.
	events, err := st.ListEvents(ctx, 1000, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 16 || count() != 17 {
		t.Fatalf("matrix size success=%d total=%d", len(all), count())
	}
	all = append(all, failed)
	for _, job := range all {
		persisted, err := st.GetJob(ctx, job.ID)
		if err != nil || persisted.Verb != job.Verb || persisted.ObjectKey != job.ObjectKey || persisted.Actor != job.Actor || persisted.Role != job.Role || persisted.CreatedBy != job.CreatedBy || bytes.Contains(persisted.Args, []byte("-----BEGIN")) {
			t.Fatalf("persisted job binding/plaintext differs for %s", job.Verb)
		}
		en, ex, correlated := 0, 0, 0
		for _, ev := range events {
			var d map[string]any
			json.Unmarshal(ev.Detail, &d)
			if d["job_id"] != job.ID {
				continue
			}
			correlated++
			outcome, code := "ok", ""
			if job.ID == failed.ID && d["stage"] == "exec" {
				outcome, code = "error", "agent_error"
			}
			if ev.Verb != job.Verb || ev.ObjectKey != job.ObjectKey || ev.Role != job.Role || ev.Actor != job.Actor || ev.Outcome != outcome || ev.Code != code || len(d) != 2 {
				t.Fatalf("audit binding/code/detail differs for %s stage=%v", job.Verb, d["stage"])
			}
			if d["stage"] == "enqueue" {
				en++
			}
			if d["stage"] == "exec" {
				ex++
			}
		}
		if en != 1 || ex != 1 || correlated != 2 {
			t.Fatalf("%s audit enqueue=%d exec=%d", job.Verb, en, ex)
		}
	}
	// Three synchronous preference changes each have an attempt and completion,
	// neither correlated to a queue job.
	autoStages := map[string]int{}
	for _, ev := range events {
		if ev.Verb != "ssl.auto_set" || ev.Actor != "untrusted-label" {
			continue
		}
		var d map[string]any
		if err := json.Unmarshal(ev.Detail, &d); err != nil {
			t.Fatal(err)
		}
		if ev.ObjectKey != "" || ev.Outcome != "ok" || ev.Code != "" || ev.Role != "owner" || len(d) != 1 {
			t.Fatal("synchronous auto_set audit differs")
		}
		stage, _ := d["stage"].(string)
		autoStages[stage]++
	}
	if len(autoStages) != 2 || autoStages["authz"] != 3 || autoStages["exec"] != 3 {
		t.Fatal("synchronous auto_set stage counts differ", autoStages)
	}
	for i, incoming := range refusalCallers {
		actor := []string{"owner", "token-owner [REDACTED]"}[i]
		for _, verb := range verbs {
			n := 0
			for _, ev := range events {
				if ev.Verb != verb || ev.Actor != actor {
					continue
				}
				var d map[string]any
				if err := json.Unmarshal(ev.Detail, &d); err != nil {
					t.Fatal(err)
				}
				if ev.Role != incoming.Role || ev.ObjectKey != "" || ev.Outcome != "refused" || ev.Code != "forbidden" || d["stage"] != "authz" || len(d) != 1 {
					t.Fatalf("%s refusal binding/stage differs", verb)
				}
				n++
			}
			if n != 1 {
				t.Fatalf("%s refusal audit count=%d", verb, n)
			}
		}
	}
	file, err := os.ReadFile(l.root + "/audit.log")
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(file), []byte("\n"))
	if len(lines) != len(events) {
		t.Fatal("file/SQLite audit count mismatch")
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var v struct {
			ID        string          `json:"id"`
			TS        int64           `json:"ts"`
			Actor     string          `json:"actor"`
			Role      string          `json:"role"`
			Verb      string          `json:"verb"`
			ObjectKey string          `json:"object_key"`
			Outcome   string          `json:"outcome"`
			Code      string          `json:"code"`
			Detail    json.RawMessage `json:"detail"`
		}
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatal(err)
		}
		if seen[v.ID] || v.ID == "" {
			t.Fatal("duplicate/empty audit mirror ID")
		}
		seen[v.ID] = true
		found := false
		for _, ev := range events {
			if ev.ID == v.ID {
				found = true
				if ev.TS != v.TS || ev.Actor != v.Actor || ev.Role != v.Role || ev.Verb != v.Verb || ev.ObjectKey != v.ObjectKey || ev.Outcome != v.Outcome || ev.Code != v.Code || !bytes.Equal(ev.Detail, v.Detail) {
					t.Fatal("audit mirror differs")
				}
			}
		}
		if !found {
			t.Fatal("file-only audit event")
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
		if bytes.Contains(data, cp) || bytes.Contains(data, kp) || bytes.Contains(data, []byte("-----BEGIN")) || bytes.Contains(data, []byte("npat_acceptance_canary")) {
			t.Fatal("PEM persisted in", path)
		}
	}
	t.Logf("real queue mutation matrix verified %d successful jobs and one intentional failure; exact correlation, refusal, mirror and redaction checks passed", len(all)-1)
}

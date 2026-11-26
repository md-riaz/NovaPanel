package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/deploy"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

type cronFailureRunner struct {
	runner.Runner
	called bool
}

func (r *cronFailureRunner) Run(ctx context.Context, program string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if program == "/usr/bin/systemctl" {
		r.called = true
		return nil, nil, -1, errors.New("systemctl unavailable")
	}
	return r.Runner.Run(ctx, program, args, opts)
}

type siteDeleteDeployRunner struct {
	t      *testing.T
	called bool
}

func (r *siteDeleteDeployRunner) Run(_ context.Context, _ string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if len(args) != 1 || args[0] != "__deploy" || opts.Uid != 1001 || opts.Gid != 1001 || len(opts.Groups) != 0 {
		r.t.Fatal("teardown did not use site credentials")
	}
	var req deploy.Request
	if err := json.NewDecoder(opts.Stdin).Decode(&req); err != nil {
		r.t.Fatal(err)
	}
	if req.Operation != "teardown" {
		r.t.Fatalf("operation %q", req.Operation)
	}
	r.called = true
	return nil, nil, 0, deploy.Apply(req)
}
func TestSiteMutations(t *testing.T) {
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
	a.Cfg.SitesRoot = filepath.Join(root, "sites")
	a.Cfg.DBPath = filepath.Join(root, "panel.db")
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
	for _, p := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(root, "nginx", p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	commands := &siteRunner{}
	a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: commands, Probe: func(context.Context, nginx.Desired) error { return nil }}
	sid, _ := id.New()
	did, _ := id.New()
	teardown := &siteDeleteDeployRunner{t: t}
	a.Exec = &accountRunner{root: a.Cfg.SitesRoot, name: "demo", id: sid, marker: "novapanel-site=", other: teardown}
	site, err := st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: filepath.Join(a.Cfg.SitesRoot, "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	if err := a.Jobs.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(verb string, args any) rpc.Reply {
		b, _ := json.Marshal(args)
		return s.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: verb, Args: b})
	}
	exec := func(verb string, args any) store.Job {
		t.Helper()
		r := call(verb, args)
		if !r.OK {
			t.Fatalf("%s: %+v", verb, r)
		}
		en := r.Data.(jobs.EnqueueResult)
		if en.Job.ObjectKey != "site:"+sid {
			t.Fatalf("wrong lock %+v", en.Job)
		}
		j := jobs.Job(en.Job)
		if e := rpc.Registry[verb].Exec(ctx, a, &j); e != nil {
			t.Fatalf("%s exec: %v", verb, e)
		}
		first := commands.calls
		retry := jobs.Job(en.Job)
		if e := rpc.Registry[verb].Exec(ctx, a, &retry); e != nil {
			t.Fatalf("%s retry: %v", verb, e)
		}
		if commands.calls != first {
			t.Fatalf("%s retry reloaded", verb)
		}
		return store.Job(j)
	}
	dbID, _ := id.New()
	userID, _ := id.New()
	if err := st.ReserveDatabase(ctx, store.Database{ID: dbID, SiteID: &sid, Name: "attached_db", Engine: "mariadb"}, store.DatabaseUser{ID: userID, DatabaseID: dbID, Username: "attached_user"}); err != nil {
		t.Fatal(err)
	}
	deleteReply := call("site.delete", map[string]any{"site_id": sid})
	if deleteReply.Code != "precondition_failed" {
		t.Fatalf("site.delete must refuse attached database: %+v", deleteReply)
	}
	if err := st.DeleteDatabase(ctx, dbID, "attached_db"); err != nil {
		t.Fatal(err)
	}
	exec("site.update", map[string]any{"site_id": sid, "maintenance": true})
	got, _ := st.SiteByID(ctx, sid)
	if !got.Maintenance {
		t.Fatal("maintenance not persisted")
	}
	exec("site.update", map[string]any{"site_id": sid, "maintenance": false})
	j := exec("domain.add", map[string]any{"site_id": sid, "fqdn": "alias.test"})
	var added struct {
		ID string `json:"domain_id"`
	}
	if err = json.Unmarshal(j.Result, &added); err != nil || added.ID == "" {
		t.Fatalf("domain result %s", j.Result)
	}
	if r := call("domain.remove", map[string]any{"domain_id": did}); r.Code != "precondition_failed" {
		t.Fatalf("primary removal %+v", r)
	}
	// Promotion uses the existing queued update contract and retains both domains.
	exec("site.update", map[string]any{"site_id": sid, "primary_domain_id": added.ID})
	promoted, _ := st.DomainByID(ctx, added.ID)
	if !promoted.IsPrimary {
		t.Fatal("promotion not persisted")
	}
	if r := call("domain.remove", map[string]any{"domain_id": added.ID}); r.Code != "precondition_failed" {
		t.Fatalf("new primary removal %+v", r)
	}
	for _, target := range []string{"bad", "01ARZ3NDEKTSV4RRFFQ69G5FAV"} {
		r := call("site.update", map[string]any{"site_id": sid, "primary_domain_id": target})
		if r.OK {
			t.Fatalf("invalid target admitted: %+v", r)
		}
	}
	exec("site.update", map[string]any{"site_id": sid, "primary_domain_id": did})
	pending := call("site.update", map[string]any{"site_id": sid, "primary_domain_id": added.ID})
	if !pending.OK {
		t.Fatalf("promotion admission %+v", pending)
	}
	// A TLS transition after enqueue must refuse execution without changing primary.
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sites SET cert_source='manual' WHERE id=?", sid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	blocked := jobs.Job(pending.Data.(jobs.EnqueueResult).Job)
	if e := rpc.Registry["site.update"].Exec(ctx, a, &blocked); e == nil {
		t.Fatal("TLS promotion executed")
	}
	if r := call("site.update", map[string]any{"site_id": sid, "primary_domain_id": added.ID}); r.Code != "precondition_failed" {
		t.Fatalf("TLS promotion admitted %+v", r)
	}
	original, _ := st.DomainByID(ctx, did)
	if !original.IsPrimary {
		t.Fatal("TLS refusal changed primary")
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE sites SET cert_source='none' WHERE id=?", sid)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	exec("domain.remove", map[string]any{"domain_id": added.ID})
	pj := jobs.Job(pending.Data.(jobs.EnqueueResult).Job)
	if e := rpc.Registry["site.update"].Exec(ctx, a, &pj); e == nil {
		t.Fatal("removed target accepted at execution")
	}
	j = exec("redirect.add", map[string]any{"site_id": sid, "source": "/old", "target": "/new", "code": 301})
	var redirect struct {
		ID string `json:"redirect_id"`
	}
	json.Unmarshal(j.Result, &redirect)
	bytes, err := os.ReadFile(filepath.Join(root, "nginx", "sites-available", "demo.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(bytes), "return 301 \"/new\";") {
		t.Fatalf("redirect not rendered %s", bytes)
	}
	firstCalls := commands.calls
	retried := exec("redirect.add", map[string]any{"site_id": sid, "source": "/old", "target": "/new", "code": 301})
	if string(retried.Result) != string(j.Result) || commands.calls != firstCalls {
		t.Fatalf("fresh redirect retry changed result or reloaded: %s / %s", retried.Result, j.Result)
	}
	rs, err := st.ListRedirects(ctx, sid)
	if err != nil || len(rs) != 1 || rs[0].ID != redirect.ID {
		t.Fatalf("redirect retry persisted duplicates: %+v %v", rs, err)
	}
	exec("redirect.remove", map[string]any{"redirect_id": redirect.ID})
	if r := call("site.update", map[string]any{"site_id": sid, "force_https": true}); r.Code != "precondition_failed" {
		t.Fatalf("TLS gate %+v", r)
	}
	if r := call("redirect.add", map[string]any{"site_id": sid, "source": "/x;evil", "target": "/new", "code": 301}); r.Code != "invalid_argument" {
		t.Fatalf("source validation %+v", r)
	}
	if r := call("deploy.configure", map[string]any{"site_id": sid, "repo_url": "https://example.com/repo", "branch": "main"}); !r.OK {
		t.Fatalf("deploy configure: %+v", r)
	}
	actualDeployDir := filepath.Join(a.Cfg.SitesRoot, "demo", ".novapanel", "deploy")
	if err = os.MkdirAll(actualDeployDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(actualDeployDir, "id_ed25519"), []byte("crash residue"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err = os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(actualDeployDir, "known_hosts")); err != nil {
		t.Fatal(err)
	}
	deployDir := filepath.Join(root, "deploy", sid)
	if err = os.MkdirAll(deployDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(deployDir, "identity"), []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "INSERT INTO deploys(id,site_id,repo_url,branch,sha,state,created) VALUES(?,?,?,?,?,?,?)", "deploy", sid, "https://example.com/repo", "main", "sha", "done", 1)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "failed"); err != nil {
		t.Fatal(err)
	}
	r := call("site.delete", map[string]any{"site_id": sid})
	if !r.OK {
		t.Fatalf("delete admission %+v", r)
	}
	en := r.Data.(jobs.EnqueueResult)
	dj, err := st.StartJob(ctx, en.Job.ID, a.Now())
	if err != nil {
		t.Fatal(err)
	}
	cronID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	cronRow := store.CronJob{ID: cronID, SiteID: sid, Schedule: "daily", ArgvJSON: `["/bin/true"]`, UnitName: "novapanel-cron-" + sid + "-" + cronID, Created: 1}
	if err = st.Tx(ctx, func(tx *sql.Tx) error { return st.InsertCronTx(ctx, tx, cronRow) }); err != nil {
		t.Fatal(err)
	}
	cronFailure := &cronFailureRunner{Runner: a.Exec}
	a.Exec = cronFailure
	// Cleanup failure must retain site metadata for a retry.
	a.Nginx = nil
	if err = rpc.Registry["site.delete"].Exec(ctx, a, &dj); err == nil {
		t.Fatal("cleanup unexpectedly succeeded")
	}
	got, err = st.SiteByID(ctx, sid)
	if err != nil || got.Status != "failed" {
		t.Fatalf("delete lifecycle %+v %v", got, err)
	}
	a.Nginx = &nginx.Applier{Root: filepath.Join(root, "nginx"), Runner: commands, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	if err = os.MkdirAll(a.Cfg.TLSDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = rpc.Registry["site.delete"].Exec(ctx, a, &dj); err != nil {
		t.Fatal(err)
	}
	if !teardown.called {
		t.Fatal("site-user deploy teardown was not called")
	}
	if !cronFailure.called {
		t.Fatal("cron systemctl failure was not exercised")
	}
	if _, err = os.Lstat(actualDeployDir); !os.IsNotExist(err) {
		t.Fatalf("deploy state remains: %v", err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
		t.Fatalf("symlink target changed: %v", err)
	}
	if err = rpc.Registry["site.delete"].Exec(ctx, a, &dj); err != nil {
		t.Fatalf("delete retry %v", err)
	}
	if err = st.CompleteOperation(ctx, dj.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SiteByID(ctx, sid); err == nil {
		t.Fatal("site remains")
	}
	if _, err = st.CronByID(ctx, cronID); err != sql.ErrNoRows {
		t.Fatalf("cron row remains: %v", err)
	}
	if _, err = st.DeployKey(ctx, sid); err != sql.ErrNoRows {
		t.Fatalf("deploy key remains: %v", err)
	}
	if _, err = st.DeployConfig(ctx, sid); err != sql.ErrNoRows {
		t.Fatalf("deploy config remains: %v", err)
	}
	if rows, err := st.DeployHistory(ctx, sid, 100, false); err != nil || len(rows) != 0 {
		t.Fatalf("deploy history remains: %v %v", rows, err)
	}
	if _, err = os.Stat(filepath.Join(deployDir, "identity")); !os.IsNotExist(err) {
		t.Fatalf("deploy files remain: %v", err)
	}
	_ = site
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

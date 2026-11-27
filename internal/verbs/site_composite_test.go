package verbs

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runnerexec "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/render/nginx"
	"novapanel/internal/store"
)

func TestCompositeExecutorCommitsParent(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "replay"}[replay], func(t *testing.T) { testCompositeExecutor(t, replay, false, false) })
	}
	t.Run("productive_failure", func(t *testing.T) { testCompositeExecutor(t, false, true, false) })
	t.Run("failure_replay", func(t *testing.T) { testCompositeExecutor(t, true, true, false) })
	t.Run("restore_retry", func(t *testing.T) { testCompositeExecutor(t, false, false, true) })
	t.Run("success_maintenance_off", func(t *testing.T) { testCompositeExecutorMaintenance(t, false, false, false, false) })
	t.Run("failure_maintenance_off", func(t *testing.T) { testCompositeExecutorMaintenance(t, false, true, false, false) })
	t.Run("failure_replay_maintenance_off", func(t *testing.T) { testCompositeExecutorMaintenance(t, true, true, false, false) })
	t.Run("restore_retry_maintenance_off", func(t *testing.T) { testCompositeExecutorMaintenance(t, false, false, true, false) })
}

func TestQuarantinedTLSAllowsMaintenanceFinalizer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	jid, _ := id.New()
	if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	force, maintenance := true, false
	request := &store.CompositeSiteUpdate{SiteID: sid, ForceHTTPS: &force, Maintenance: &maintenance}
	j := store.Job{ID: jid, Verb: "site.update", ObjectKey: "site:" + sid, CreatedBy: "local-root", Role: "owner", State: "queued", Args: []byte(`{}`)}
	if _, err = st.PrepareOperation(ctx, j, store.JobClaim{}, store.OperationRequest{Kind: store.SiteUpdate, SiteID: sid, SiteUpdate: request}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = st.StartJob(ctx, jid, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	on := true
	if err = st.UpdateSite(ctx, sid, store.SitePatch{Maintenance: &on}); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"job_id":"` + jid + `"}`)
	if err = st.BeginCompositeTLSIntent(ctx, jid, raw); err != nil {
		t.Fatal(err)
	}
	if blocked, err := st.FinalizerTLSBlocked(ctx, jid); err != nil || !blocked {
		t.Fatalf("valid pending intent must block finalizer: %v %v", blocked, err)
	}
	if err = st.UpdateMaintenanceFinalizer(ctx, sid, jid, false); err == nil {
		t.Fatal("valid pending intent allowed maintenance update")
	}
	if err = st.ClearTLSIntent(ctx, raw); err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte{}, raw...), []byte(` {}`)...)
	if err = st.BeginTLSIntent(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if matched, changed, err := st.CheckCompositeTLSIntent(ctx, changed); err != nil || !matched || !changed {
		t.Fatalf("quarantine: %v %v %v", matched, changed, err)
	}
	a := agent.New()
	a.Store = st
	a.Nginx.Root = filepath.Join(root, "nginx")
	a.Nginx.Runner = poolReplayRunner{}
	a.Nginx.Probe = func(context.Context, nginx.Desired) error { return nil }
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(a.Nginx.Root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	children, err := st.OperationChildren(ctx, jid)
	if err != nil {
		t.Fatal(err)
	}
	job, err := st.GetJob(ctx, jid)
	if err != nil {
		t.Fatal(err)
	}
	if err = runCompositeEffect(ctx, a, &job, siteUpdateArgs{SiteID: sid, Maintenance: &maintenance}, children[len(children)-1]); err != nil {
		t.Fatal(err)
	}
	if err = st.CommitOperationChild(ctx, jid, children[len(children)-1].Ordinal, "verified", "committed", ""); err != nil {
		t.Fatal(err)
	}
	site, err := st.SiteByID(ctx, sid)
	if err != nil || site.Maintenance {
		t.Fatalf("maintenance: %+v %v", site, err)
	}
	op, err := st.LoadOperation(ctx, jid)
	if err != nil || op.Reconciled || op.Checkpoint == "done" || op.Checkpoint == "expired" {
		t.Fatalf("operation: %+v %v", op, err)
	}
	job, err = st.GetJob(ctx, jid)
	if err != nil || job.Code != "agent_error" || job.State != "running" {
		t.Fatalf("job: %+v %v", job, err)
	}
	intent, err := st.TLSIntent(ctx)
	if err != nil || string(intent) != string(changed) {
		t.Fatalf("intent: %s %v", intent, err)
	}
}

type compositeRetryRunner struct{ calls atomic.Int32 }

func (r *compositeRetryRunner) Run(ctx context.Context, program string, args []string, opts runnerexec.RunOpts) ([]byte, []byte, int, error) {
	if program == "/usr/sbin/nginx" && r.calls.Add(1) == 1 {
		return nil, nil, 1, nil
	}
	return nil, nil, 0, nil
}

func TestCompositeBracketCrashBeforeChildCommit(t *testing.T) {
	for _, stop := range []int{0, 1, 2, 3, 4, 5} {
		t.Run([]string{"maintenance_effect", "maintenance_committed", "php_committed", "domain_committed", "restore_committed", "productive_failure"}[stop], func(t *testing.T) { testCompositeBracketCrash(t, stop) })
	}
}

func testCompositeBracketCrash(t *testing.T, stop int) {
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
	sid, _ := id.New()
	did, _ := id.New()
	version := "8.3"
	if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", PHPVersion: &version, Docroot: filepath.Join(root, "sites", "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	pool := filepath.Join(root, "pool")
	if err = os.Mkdir(pool, 0755); err != nil {
		t.Fatal(err)
	}
	master := "[global]\npid = /run/php/php8.3-fpm.pid\nerror_log = /var/log/php8.3-fpm.log\ninclude = " + filepath.Join(pool, "*.conf") + "\n"
	if err = os.WriteFile(filepath.Join(root, "php-fpm.conf"), []byte(master), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pool, "novapanel-demo.conf"), []byte("; novapanel-managed site="+sid+"\ninitial"), 0644); err != nil {
		t.Fatal(err)
	}
	ngroot := filepath.Join(root, "nginx")
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(ngroot, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	runner := poolReplayRunner{filepath.Join(root, "sites"), sid}
	a := agent.New()
	a.Store, a.Exec, a.Cfg.SitesRoot = st, runner, runner.root
	a.PHP = &php.PoolApplier{Dir: pool, Runner: runner, Probe: func(context.Context) error { return nil }}
	a.Nginx = &nginx.Applier{Root: ngroot, Runner: runner, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.Jobs = jobs.NewQueue(st, nil, log)
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { return siteUpdateExec(ctx, a, j) })
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.Jobs.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	owner := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "owner"})
	admitted, err := siteUpdate(owner, a, &siteUpdateArgs{SiteID: sid, PHPVersion: &version, PrimaryDomainID: &did})
	if err != nil {
		t.Fatal(err)
	}
	result := admitted.(jobs.EnqueueResult)
	if _, err = st.StartJob(ctx, result.Job.ID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = st.EngageOperationBracket(ctx, result.Job.ID); err != nil {
		t.Fatal(err)
	}
	maintenance := true
	if err = st.UpdateSite(ctx, sid, store.SitePatch{Maintenance: &maintenance}); err != nil {
		t.Fatal(err)
	}
	if stop >= 1 {
		if err = st.CommitOperationChild(ctx, result.Job.ID, 0, "verified", "committed", ""); err != nil {
			t.Fatal(err)
		}
	}
	if stop == 5 {
		if err = st.CommitOperationChild(ctx, result.Job.ID, 1, "prepared", "failed", "precondition_failed"); err != nil {
			t.Fatal(err)
		}
	}
	if stop >= 2 && stop < 5 {
		if err = st.CheckpointOperationChild(ctx, result.Job.ID, 1, "prepared", "config_published"); err != nil {
			t.Fatal(err)
		}
		if err = st.CheckpointOperationChild(ctx, result.Job.ID, 1, "config_published", "verified"); err != nil {
			t.Fatal(err)
		}
		if err = st.CommitPHPChild(ctx, result.Job.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	if stop >= 3 && stop < 5 {
		if err = st.CommitOperationChild(ctx, result.Job.ID, 2, "verified", "committed", ""); err != nil {
			t.Fatal(err)
		}
	}
	if stop == 4 {
		maintenance = false
		if err = st.UpdateSite(ctx, sid, store.SitePatch{Maintenance: &maintenance}); err != nil {
			t.Fatal(err)
		}
		if err = st.CommitOperationChild(ctx, result.Job.ID, 3, "verified", "committed", ""); err != nil {
			t.Fatal(err)
		}
	}
	before, err := st.OperationChildren(ctx, result.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := st.GetJob(ctx, result.Job.ID)
	wantState := "done"
	if stop == 5 {
		wantState = "failed"
	}
	if err != nil || j.State != wantState {
		t.Fatalf("job: %+v %v", j, err)
	}
	site, err := st.SiteByID(ctx, sid)
	if err != nil || site.Maintenance {
		t.Fatalf("maintenance not restored: %+v %v", site, err)
	}
	children, err := st.OperationChildren(ctx, j.ID)
	if err != nil || len(children) != 4 {
		t.Fatalf("children: %+v %v", children, err)
	}
	for i, child := range children {
		wantStatus, wantError := "committed", ""
		if stop == 5 && i == 1 {
			wantStatus, wantError = "failed", "precondition_failed"
		} else if stop == 5 && i == 2 {
			wantStatus, wantError = "failed", "not_attempted"
		}
		if child.Status != wantStatus || child.TypedError != wantError {
			t.Fatalf("child: %+v", child)
		}
		if before[i].Status == "committed" && before[i].JournalRef != child.JournalRef {
			t.Fatalf("child %d reran", i)
		}
	}
}

func testCompositeExecutor(t *testing.T, replay, failure, retry bool) {
	testCompositeExecutorMaintenance(t, replay, failure, retry, true)
}

func testCompositeExecutorMaintenance(t *testing.T, replay, failure, retry, maintenance bool) {
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
	sid, _ := id.New()
	did, _ := id.New()
	if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "demo.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	ngroot := filepath.Join(root, "nginx")
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err = os.MkdirAll(filepath.Join(ngroot, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	a := agent.New()
	a.Store = st
	a.Nginx = &nginx.Applier{Root: ngroot, Runner: poolReplayRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
	if retry {
		a.Nginx.Runner = &compositeRetryRunner{}
	}
	a.Jobs = jobs.NewQueue(st, nil, log)
	request := &store.CompositeSiteUpdate{SiteID: sid, PrimaryDomainID: &did, Maintenance: &maintenance}
	if failure {
		force := true
		request.PrimaryDomainID, request.ForceHTTPS = nil, &force
	}
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { return siteUpdateExec(ctx, a, j) })
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if err = a.Jobs.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	owner := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "owner"})
	result, err := a.Jobs.EnqueueOperation(owner, store.OperationRequest{Kind: store.SiteUpdate, SiteID: sid, SiteUpdate: request}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var originalJournal string
	if replay {
		if _, err = st.StartJob(ctx, result.Job.ID, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		status, code := "committed", ""
		if failure {
			status, code = "failed", "precondition_failed"
		}
		if err = st.CommitOperationChild(ctx, result.Job.ID, 0, "verified", status, code); err != nil {
			t.Fatal(err)
		}
		children, err := st.OperationChildren(ctx, result.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		originalJournal = children[0].JournalRef
		if err = a.Jobs.PrepareOperations(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := st.GetJob(ctx, result.Job.ID)
		if err == nil && (j.State == "done" || failure && j.State == "failed") {
			if retry && j.Attempts < 2 {
				t.Fatalf("restore was not retried: %+v", j)
			}
			site, err := st.SiteByID(ctx, sid)
			if err != nil || site.Maintenance != maintenance {
				t.Fatalf("maintenance: %+v %v", site, err)
			}
			children, err := st.OperationChildren(ctx, j.ID)
			first := "committed"
			if failure {
				first = "failed"
			}
			if err != nil || len(children) != 2 || children[0].Status != first || children[1].Status != "committed" {
				t.Fatalf("children: %+v %v", children, err)
			}
			if replay && children[0].JournalRef != originalJournal {
				t.Fatal("committed child was rerun")
			}
			return
		}
		if err == nil && j.State == "failed" {
			t.Fatalf("failed: %+v", j)
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, err := st.GetJob(ctx, result.Job.ID)
	children, childErr := st.OperationChildren(ctx, result.Job.ID)
	t.Fatalf("composite did not finish: job=%+v err=%v children=%+v childErr=%v", j, err, children, childErr)
}

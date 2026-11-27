package verbs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/render/nginx"
	"novapanel/internal/store"
)

func TestCompositeExecutorCommitsParent(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "replay"}[replay], func(t *testing.T) { testCompositeExecutor(t, replay, false) })
	}
	t.Run("productive_failure", func(t *testing.T) { testCompositeExecutor(t, false, true) })
}

func TestCompositeBracketCrashBeforeChildCommit(t *testing.T) {
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
	v := siteUpdateArgs{SiteID: sid, PHPVersion: &version, PrimaryDomainID: &did}
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { return siteCompositeExec(ctx, a, j, v) })
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	owner := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "owner"})
	result, err := a.Jobs.EnqueueOperation(owner, store.OperationRequest{Kind: store.SiteUpdate, SiteID: sid, SiteUpdate: &store.CompositeSiteUpdate{SiteID: sid, PHPVersion: &version, PrimaryDomainID: &did}}, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := st.GetJob(ctx, result.Job.ID)
	if err != nil || j.State != "done" {
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
	for _, child := range children {
		if child.Status != "committed" {
			t.Fatalf("child: %+v", child)
		}
	}
}

func testCompositeExecutor(t *testing.T, replay, failure bool) {
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
	a.Jobs = jobs.NewQueue(st, nil, log)
	maintenance := true
	v := siteUpdateArgs{SiteID: sid, PrimaryDomainID: &did, Maintenance: &maintenance}
	request := &store.CompositeSiteUpdate{SiteID: sid, PrimaryDomainID: &did, Maintenance: &maintenance}
	if failure {
		force := true
		v.PrimaryDomainID, v.ForceHTTPS = nil, &force
		request.PrimaryDomainID, request.ForceHTTPS = nil, &force
	}
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { return siteCompositeExec(ctx, a, j, v) })
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
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
		if err = st.CommitOperationChild(ctx, result.Job.ID, 0, "verified", "committed", ""); err != nil {
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
			site, err := st.SiteByID(ctx, sid)
			if err != nil || !site.Maintenance {
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
	t.Fatal("composite did not finish")
}

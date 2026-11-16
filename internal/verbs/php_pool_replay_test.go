package verbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	renderphp "novapanel/internal/render/php"
	"novapanel/internal/store"
	"time"
)

type poolReplayRunner struct{ root, id string }

func (r poolReplayRunner) Run(_ context.Context, prog string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" {
		return []byte("demo:x:1001:1001:novapanel-site=" + r.id + ":" + filepath.Join(r.root, "demo") + ":/usr/sbin/nologin\n"), nil, 0, nil
	}
	if prog == "/usr/bin/systemctl" && len(args) > 0 && args[0] == "show" {
		return []byte("loaded\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}

func TestPHPSetUsesCommittedLimits(t *testing.T) {
	testPHPSetReplay(t, false)
}

func TestPHPSetBackfillsLegacyCap(t *testing.T) {
	testPHPSetReplay(t, true)
}

func testPHPSetReplay(t *testing.T, legacy bool) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sid, _ := id.New()
	did, _ := id.New()
	version := "8.3"
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", PHPVersion: &version, Docroot: filepath.Join(root, "sites", "demo", "public")}, store.Domain{ID: did, FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	memory, cap := 128, 5
	limitsInitial := store.PHPLimits{MemoryMB: &memory, MaxChildren: &cap}
	if legacy {
		limitsInitial.MaxChildren = nil
	}
	if err = st.SetSitePHP(ctx, sid, version, limitsInitial); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "pool")
	if err = os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	master := "[global]\npid = /run/php/php8.3-fpm.pid\nerror_log = /var/log/php8.3-fpm.log\ninclude = " + filepath.Join(dir, "*.conf") + "\n"
	if err = os.WriteFile(filepath.Join(root, "php-fpm.conf"), []byte(master), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "novapanel-demo.conf"), []byte("; novapanel-managed site="+sid+"\nuncommitted"), 0644); err != nil {
		t.Fatal(err)
	}
	r := poolReplayRunner{filepath.Join(root, "sites"), sid}
	a := agent.New()
	a.Store = st
	a.Cfg.SitesRoot = r.root
	a.Exec = r
	a.PHP = &php.PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	log, err := audit.Open(st, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a.Jobs = jobs.NewQueue(st, nil, log)
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error { return phpSetExec(ctx, a, j) })
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	upload := 32
	owner := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "owner"})
	result, err := a.Jobs.EnqueueOperation(owner, store.OperationRequest{Kind: store.PHPSet, SiteID: sid, PHP: &renderphp.SetRequest{SiteID: sid, Version: version, Limits: renderphp.LimitsPatch{UploadMB: &upload}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.LoadOperation(ctx, result.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := st.GetJob(ctx, result.Job.ID)
		if e != nil && !errors.Is(e, store.ErrPublicationPending) {
			t.Fatal(e)
		}
		if errors.Is(e, store.ErrPublicationPending) {
			if time.Now().After(deadline) {
				t.Fatal("publication remained pending")
			}
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if j.State == "done" {
			break
		}
		if j.State == "failed" {
			t.Fatalf("job failed: %+v", j)
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not complete")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	limits, err := st.SitePHPLimits(ctx, sid)
	if err != nil || limits.MaxChildren == nil || *limits.MaxChildren != before.Identity.PHP.DesiredCap || limits.MemoryMB == nil || *limits.MemoryMB != 128 || limits.UploadMB == nil || *limits.UploadMB != 32 {
		t.Fatalf("limits %+v: %v", limits, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "novapanel-demo.conf"))
	if err != nil || !strings.Contains(string(b), "memory_limit] = 128M") || !strings.Contains(string(b), "upload_max_filesize] = 32M") {
		t.Fatalf("pool %q: %v", b, err)
	}
}

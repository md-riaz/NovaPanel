//go:build wordpress_offline_lab && linux

package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	_ "novapanel/internal/verbs"
	"novapanel/internal/wordpress"
)

// The test binary also serves as the confined site-UID extraction child.
func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "__wordpress-extract" {
		if err := wordpress.ExtractFreshCore(context.Background(), os.Args[2]); err != nil {
			if errors.Is(err, wordpress.ErrDownloadRetry) {
				os.Exit(wordpress.ExtractRetryExit)
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// Run only in a disposable, network-isolated container with the approved dual
// HTTPS mirror, packaged tool and ini, MariaDB, nginx, FPM and cgroup v2 ready.
func TestFreshWordPressSiteCreateOfflineE2E(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" || os.Getenv("NOVAPANEL_FRESH_WP_MIRROR_LAB") != "1" {
		t.Skip("requires disposable root container and offline dual mirror")
	}
	ctx := context.Background()
	unique, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	name := "npt" + strings.ToLower(unique[len(unique)-12:])
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	a.Jobs.SetPostRecovery(nil)
	if err = a.Jobs.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	args, err := json.Marshal(map[string]any{"name": name, "type": "wordpress", "primary_domain": name + ".test", "wordpress": map[string]string{"title": "Offline gate", "admin_username": "admin", "admin_email": "admin@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: args})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	en := reply.Data.(jobs.EnqueueResult)
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		j, e := st.GetJob(ctx, en.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "failed" {
			op, _ := st.LoadOperation(ctx, j.ID)
			t.Fatalf("failed: %+v checkpoint=%s", j, op.Checkpoint)
		}
		if j.State == "done" {
			site, e := st.SiteByName(ctx, name)
			if e != nil || site.Status != "active" {
				t.Fatalf("site: %+v %v", site, e)
			}
			life, e := st.LoadSiteLifetime(ctx, site.ID)
			if e != nil || life.UID < 2000000 || life.UID > 2999999 {
				t.Fatalf("identity: %+v %v", life, e)
			}
			info, e := os.Stat(filepath.Join(life.Docroot, "wp-config.php"))
			if e != nil || info.Mode().Perm() != 0400 {
				t.Fatalf("config: %v %v", info, e)
			}
			for _, state := range []string{"is-enabled", "is-active"} {
				_, _, code, err := (runner.Real{}).Run(ctx, "/usr/bin/systemctl", []string{state, "--quiet", "php8.3-fpm.service"}, runner.RunOpts{})
				if err != nil || code != 0 {
					t.Fatalf("FPM %s: %v", state, err)
				}
			}
			claimCtx := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root"})
			claim, e := a.Jobs.Claim(claimCtx, j.ID, en.ClaimToken)
			if e != nil || len(claim) != 2 {
				t.Fatalf("claim: %v", e)
			}
			if _, e = a.Jobs.Claim(claimCtx, j.ID, en.ClaimToken); e == nil {
				t.Fatal("claim was reusable")
			}
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	j, _ := st.GetJob(ctx, en.Job.ID)
	op, _ := st.LoadOperation(ctx, en.Job.ID)
	t.Fatalf("timeout: %+v checkpoint=%s", j, op.Checkpoint)
}

type retryRollbackRunner struct {
	base                     runner.Real
	accountDeletes, injected atomic.Int32
}

func (r *retryRollbackRunner) Run(ctx context.Context, program string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if program == "/usr/bin/mariadb" && r.accountDeletes.Load() > 0 && r.injected.CompareAndSwap(0, 1) {
		return nil, nil, -1, errors.New("injected rollback database failure")
	}
	out, stderr, code, err := r.base.Run(ctx, program, args, opts)
	if program == "/usr/sbin/userdel" && err == nil && code == 0 {
		r.accountDeletes.Add(1)
	}
	return out, stderr, code, err
}

// TestFreshWordPressRollbackOfflineE2E requires the disposable mirror fixture
// to serve a checksum mismatch while /tmp/mirror/mismatch exists.
func TestFreshWordPressRollbackOfflineE2E(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" || os.Getenv("NOVAPANEL_FRESH_WP_ROLLBACK_LAB") != "1" {
		t.Skip("requires disposable root container and fault-injecting offline mirror")
	}
	ctx := context.Background()
	unique, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	name := "npr" + strings.ToLower(unique[len(unique)-12:])
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	fault := &retryRollbackRunner{}
	a.Exec = fault
	a.Store = st
	a.Audit = log
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	a.Jobs.SetPostRecovery(nil)
	if err = a.Jobs.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	args, err := json.Marshal(map[string]any{"name": name, "type": "wordpress", "primary_domain": name + ".test", "wordpress": map[string]string{"title": "Rollback gate", "admin_username": "admin", "admin_email": "admin@example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	create := func() jobs.EnqueueResult {
		t.Helper()
		reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: args})
		if !reply.OK {
			t.Fatalf("admission: %+v", reply)
		}
		return reply.Data.(jobs.EnqueueResult)
	}
	wait := func(en jobs.EnqueueResult) store.Job {
		t.Helper()
		deadline := time.Now().Add(180 * time.Second)
		for time.Now().Before(deadline) {
			j, e := st.GetJob(ctx, en.Job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if j.State == "failed" || j.State == "done" {
				return j
			}
			time.Sleep(250 * time.Millisecond)
		}
		j, _ := st.GetJob(ctx, en.Job.ID)
		op, _ := st.LoadOperation(ctx, en.Job.ID)
		t.Fatalf("timeout: %+v checkpoint=%s", j, op.Checkpoint)
		return store.Job{}
	}
	marker := "/tmp/mirror/mismatch"
	if err = os.WriteFile(marker, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(marker)
	first := create()
	failed := wait(first)
	if failed.State != "failed" {
		t.Fatalf("mismatch accepted: %+v", failed)
	}
	if failed.Attempts < 3 || fault.injected.Load() != 1 || fault.accountDeletes.Load() != 1 {
		t.Fatalf("rollback retry did not converge safely: attempts=%d injected=%d userdel=%d", failed.Attempts, fault.injected.Load(), fault.accountDeletes.Load())
	}

	op, err := st.LoadOperation(ctx, first.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if op.Checkpoint != "expired" || !op.Reconciled {
		t.Fatalf("rollback journal: %+v", op)
	}
	if _, err = st.SiteByName(ctx, name); err == nil {
		t.Fatal("failed site retained")
	}
	for _, root := range []string{a.Cfg.SitesRoot, a.Cfg.LogDir} {
		if _, err = os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("site tree retained at %s: %v", root, err)
		}
	}
	for _, kind := range []string{"passwd", "group"} {
		_, _, code, _ := (runner.Real{}).Run(ctx, "/usr/bin/getent", []string{kind, name}, runner.RunOpts{})
		if code != 2 {
			t.Fatalf("%s retained, exit=%d", kind, code)
		}
	}
	sql := fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s')+(SELECT COUNT(*) FROM mysql.user WHERE User='%s' AND Host='127.0.0.1')+(SELECT COUNT(*) FROM novapanel.managed_objects WHERE obj_id IN ('%s','%s'));", op.Identity.Database.Name, op.Identity.Accounts[0].Username, op.DatabaseID, op.Identity.Accounts[0].ID)
	out, _, code, err := (runner.Real{}).Run(ctx, "/usr/bin/mariadb", []string{"--no-defaults", "--protocol=socket", "--user=root", "--batch", "--skip-column-names"}, runner.RunOpts{Stdin: strings.NewReader(sql)})
	if err != nil || code != 0 || strings.TrimSpace(string(out)) != "0" {
		t.Fatalf("database residue: %s exit=%d %v", out, code, err)
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	second := create()
	completed := wait(second)
	if completed.State != "done" {
		t.Fatalf("replacement failed: %+v", completed)
	}
	site, err := st.SiteByName(ctx, name)
	if err != nil || site.Status != "active" {
		t.Fatalf("replacement site: %+v %v", site, err)
	}
	life, err := st.LoadSiteLifetime(ctx, site.ID)
	if err != nil || life.UID != 2000001 {
		t.Fatalf("UID not retired: %+v %v", life, err)
	}
	claimCtx := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root"})
	claim, err := a.Jobs.Claim(claimCtx, second.Job.ID, second.ClaimToken)
	if err != nil || len(claim) != 2 {
		t.Fatalf("replacement claim: %v", err)
	}
}

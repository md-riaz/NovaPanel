//go:build wordpress_offline_lab && linux

package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	"novapanel/internal/wordpress"
)

type preflightFailureRunner struct {
	base        runner.Real
	armed       atomic.Bool
	hit         atomic.Bool
	name, jobID string
	store       *store.Store
}

func (r *preflightFailureRunner) Run(ctx context.Context, program string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if program == "/usr/bin/getent" && len(args) == 2 && args[0] == "passwd" && args[1] == r.name && r.armed.CompareAndSwap(true, false) {
		op, err := r.store.LoadOperation(ctx, r.jobID)
		if err != nil || op.Checkpoint != "verification_in_progress" {
			return nil, nil, -1, errors.New("account preflight ran without invalidation")
		}
		r.hit.Store(true)
		return nil, nil, -1, errors.New("injected account ownership preflight failure")
	}
	return r.base.Run(ctx, program, args, opts)
}

// The hook terminates the worker only after a real checkpoint commits.
func TestFreshWordPressWorkerCheckpointRecoveryOfflineE2E(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" || os.Getenv("NOVAPANEL_FRESH_WP_MIRROR_LAB") != "1" {
		t.Skip("requires disposable root container and offline dual mirror")
	}
	ctx := context.Background()
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
	fault := &preflightFailureRunner{store: st}
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
	defer store.SetFreshCheckpointTestHook(nil)
	var pinChecks atomic.Int32
	wordpress.SetFreshArchiveTestHook(func() { pinChecks.Add(1) })
	defer wordpress.SetFreshArchiveTestHook(nil)
	checksumRequests := func() int {
		b, err := os.ReadFile("/tmp/mirror/server.log")
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "GET /core/checksums/")
	}
	create := func() (jobs.EnqueueResult, string) {
		t.Helper()
		unique, err := id.New()
		if err != nil {
			t.Fatal(err)
		}
		name := "npc" + strings.ToLower(unique[len(unique)-12:])
		args, err := json.Marshal(map[string]any{"name": name, "type": "wordpress", "primary_domain": name + ".test", "wordpress": map[string]string{"title": "Recovery", "admin_username": "admin", "admin_email": "admin@example.test"}})
		if err != nil {
			t.Fatal(err)
		}
		reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "site.create", Args: args})
		if !reply.OK {
			t.Fatalf("admission: %+v", reply)
		}
		return reply.Data.(jobs.EnqueueResult), name
	}
	start := func() (context.CancelFunc, <-chan error) {
		run, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { defer close(done); done <- a.Jobs.Run(run) }()
		return cancel, done
	}
	pause := func(target string) <-chan string {
		reached := make(chan string, 1)
		store.SetFreshCheckpointTestHook(func(_ context.Context, jid, checkpoint string) {
			if checkpoint == target {
				select {
				case reached <- jid:
					runtime.Goexit()
				default:
				}
			}
		})
		return reached
	}
	stop := func(cancel context.CancelFunc, done <-chan error, reached <-chan string, jid string) {
		t.Helper()
		select {
		case got := <-reached:
			if got != jid {
				t.Fatalf("wrong job %s", got)
			}
		case <-time.After(180 * time.Second):
			t.Fatal("checkpoint not reached")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			cancel()
			t.Fatal("worker did not stop")
		}
		cancel()
		store.SetFreshCheckpointTestHook(nil)
	}
	recoverWorker := func() {
		t.Helper()
		// A crashed worker loses its in-memory locks; construct its replacement.
		server.ConfigureJobs()
		a.Jobs.SetPostRecovery(nil)
		if err := a.Jobs.PrepareStartup(ctx); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(jid string) store.Job {
		t.Helper()
		deadline := time.Now().Add(180 * time.Second)
		for time.Now().Before(deadline) {
			j, err := st.GetJob(ctx, jid)
			if err != nil {
				t.Fatal(err)
			}
			if j.State == "done" || j.State == "failed" {
				return j
			}
			time.Sleep(100 * time.Millisecond)
		}
		j, _ := st.GetJob(ctx, jid)
		op, _ := st.LoadOperation(ctx, jid)
		t.Fatalf("timeout job=%+v checkpoint=%s", j, op.Checkpoint)
		return store.Job{}
	}
	baseline := checksumRequests()
	first, name := create()
	for i, checkpoint := range []string{"verified_resumable", "config_published", "activated"} {
		beforePins := pinChecks.Load()
		reached := pause(checkpoint)
		cancel, done := start()
		stop(cancel, done, reached, first.Job.ID)
		if i > 0 && pinChecks.Load() <= beforePins {
			t.Fatalf("resume to %s did not recheck pinned archive", checkpoint)
		}
		if got := checksumRequests(); got != baseline+1 {
			t.Fatalf("live checksum repeated at %s: %d", checkpoint, got-baseline)
		}
		recoverWorker()
	}
	pinsAtActivation := pinChecks.Load()
	cancel, done := start()
	result := wait(first.Job.ID)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if result.State != "done" || checksumRequests() != baseline+1 || pinChecks.Load() != pinsAtActivation {
		t.Fatalf("activated replay was not finalize-only: %+v", result)
	}
	site, err := st.SiteByName(ctx, name)
	if err != nil || site.Status != "active" {
		t.Fatalf("site: %+v %v", site, err)
	}
	life, err := st.LoadSiteLifetime(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(life.Docroot, "wp-config.php"))
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatalf("config: %v %v", info, err)
	}
	for _, state := range []string{"is-enabled", "is-active"} {
		_, _, code, err := (runner.Real{}).Run(ctx, "/usr/bin/systemctl", []string{state, "--quiet", "php8.3-fpm.service"}, runner.RunOpts{})
		if err != nil || code != 0 {
			t.Fatalf("FPM %s: %v", state, err)
		}
	}
	claimCtx := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root"})
	claim, err := a.Jobs.Claim(claimCtx, first.Job.ID, first.ClaimToken)
	if err != nil || len(claim) != 2 {
		t.Fatalf("claim: %v", err)
	}
	if _, err = a.Jobs.Claim(claimCtx, first.Job.ID, first.ClaimToken); err == nil {
		t.Fatal("claim was reusable")
	}

	second, name2 := create()
	reached := pause("verified_resumable")
	cancel, done = start()
	stop(cancel, done, reached, second.Job.ID)
	recoverWorker()
	// Fail a real account check after the committed invalidation, and refuse
	// the rollback fence so startup must recover from the preflight state.
	fault.name, fault.jobID = name2, second.Job.ID
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER test_fail_preflight_fence BEFORE UPDATE OF step ON jobs WHEN NEW.step='wordpress_rollback_intent' BEGIN SELECT RAISE(FAIL,'injected rollback fence failure'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fault.armed.Store(true)
	cancel, done = start()
	select {
	case err := <-done:
		if !errors.Is(err, jobs.ErrRetry) {
			t.Fatalf("preflight fence failure: %v", err)
		}
	case <-time.After(180 * time.Second):
		cancel()
		t.Fatal("preflight failure did not stop worker")
	}
	cancel()
	if !fault.hit.Load() {
		t.Fatal("account preflight was not exercised")
	}
	op, err := st.LoadOperation(ctx, second.Job.ID)
	if err != nil || op.Checkpoint != "verification_in_progress" {
		t.Fatalf("preflight checkpoint=%s err=%v", op.Checkpoint, err)
	}
	if err = st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TRIGGER test_fail_preflight_fence`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	recoverWorker()
	j, err := st.GetJob(ctx, second.Job.ID)
	if err != nil || j.Step != store.FreshWordPressRollbackIntentStep {
		t.Fatalf("not routed to rollback: %+v %v", j, err)
	}
	cancel, done = start()
	result = wait(second.Job.ID)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if result.State != "failed" {
		t.Fatalf("preflight crash resumed: %+v", result)
	}
	if _, err := st.SiteByName(ctx, name2); err == nil {
		t.Fatal("rollback retained site")
	}
	for _, root := range []string{a.Cfg.SitesRoot, a.Cfg.LogDir} {
		if _, err := os.Lstat(filepath.Join(root, name2)); !os.IsNotExist(err) {
			t.Fatalf("rollback retained tree at %s: %v", root, err)
		}
	}
	for _, kind := range []string{"passwd", "group"} {
		_, _, code, _ := (runner.Real{}).Run(ctx, "/usr/bin/getent", []string{kind, name2}, runner.RunOpts{})
		if code != 2 {
			t.Fatalf("rollback retained %s, exit=%d", kind, code)
		}
	}
	if got := checksumRequests(); got != baseline+2 {
		t.Fatalf("preflight restart re-ran live checksums: %d", got-baseline)
	}
}

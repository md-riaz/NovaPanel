//go:build wordpress_offline_lab && linux

package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	a.Jobs.SetConfigCustody("/var/lib/novapanel/wordpress-config")
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

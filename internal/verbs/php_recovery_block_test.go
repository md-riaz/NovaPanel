package verbs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type phpBrokenRunner struct{}

func (phpBrokenRunner) Run(context.Context, string, []string, runner.RunOpts) ([]byte, []byte, int, error) {
	return nil, nil, 1, errors.New("injected command failure")
}

// A failed rollback must not be finalized as a normal failed job: the original
// job and its snapshot are still required for recovery after process restart.
func TestPHPQueueBlocksOnUnresolvedRollback(t *testing.T) {
	requireRootPHPCustody(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	for _, d := range []string{"sites-available", "sites-enabled", "pool", "journal"} {
		if err := os.Mkdir(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
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
	a.Store, a.Audit = st, log
	a.Nginx = &nginx.Applier{Root: root, Runner: phpBrokenRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.PHP = &agent.PHPDependencies{Pools: &php.Applier{Root: filepath.Join(root, "pool"), Runner: phpBrokenRunner{}, Probe: func(context.Context, php.Desired) error { return nil }}, RecoveryRoot: filepath.Join(root, "journal"), ResolveIdentity: func(context.Context, store.Site) (uint32, uint32, error) { return 1001, 1001, nil }}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	version := "8.3"
	if _, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "admin-session", UserID: "admin-user"}), rpc.Request{Verb: "php.set", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512}}`, sid))})
	if !reply.OK {
		t.Fatalf("dispatch: %+v", reply)
	}
	job := reply.Data.(jobs.EnqueueResult).Job
	err = a.Jobs.Run(ctx)
	if !errors.Is(err, jobs.ErrRecoveryBlocked) {
		t.Fatalf("queue must block on unresolved rollback: %v", err)
	}
	got, err := st.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "running" {
		t.Fatalf("job must retain recovery ownership: %+v", got)
	}
	op, err := st.LoadOperation(context.Background(), job.ID)
	if err != nil || op.Checkpoint == "done" || op.Checkpoint == "expired" {
		t.Fatalf("journal must remain recoverable: %+v %v", op, err)
	}
	if _, err = os.Stat(filepath.Join(root, "journal", job.ID+".json")); err != nil {
		t.Fatal(err)
	}
}

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
	"strings"
)

type phpScratchRunner struct{}

func (phpScratchRunner) Run(context.Context, string, []string, runner.RunOpts) ([]byte, []byte, int, error) {
	return nil, nil, 0, nil
}

func requireRootPHPCustody(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root-owned recovery custody; exercised separately in CI")
	}
}

// This exercises the dispatcher and the real queue, not a direct executor call.
func TestPHPSetQueuePublishesScratchConfiguration(t *testing.T) {
	requireRootPHPCustody(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled", "pool", "journal"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	log, err := audit.Open(st, auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store, a.Audit = st, log
	a.Nginx = &nginx.Applier{Root: root, Runner: phpScratchRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.PHP = &agent.PHPDependencies{Pools: &php.Applier{Root: filepath.Join(root, "pool"), Runner: phpScratchRunner{}, Probe: func(context.Context, php.Desired) error { return nil }}, RecoveryRoot: filepath.Join(root, "journal"), ResolveIdentity: func(context.Context, store.Site) (uint32, uint32, error) { return 1001, 1001, nil }}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	version := "8.3"
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	caller := rpc.Caller{Kind: "user", Role: "admin", Session: "admin-session", UserID: "admin-user"}
	reply := server.Dispatch(rpc.WithCaller(ctx, caller), rpc.Request{Verb: "php.set", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512}}`, sid))})
	if !reply.OK {
		t.Fatalf("dispatch: %+v", reply)
	}
	enqueued, ok := reply.Data.(jobs.EnqueueResult)
	if !ok {
		t.Fatalf("enqueue result: %#v", reply.Data)
	}
	done := make(chan error, 1)
	workerCtx, stop := context.WithCancel(ctx)
	go func() { done <- a.Jobs.Run(workerCtx) }()
	defer func() { stop(); <-done }()
	for {
		j, err := st.GetJob(ctx, enqueued.Job.ID)
		if err != nil {
			if errors.Is(err, store.ErrPublicationPending) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			t.Fatal(err)
		}
		if j.State == "failed" {
			t.Fatalf("PHP publication failed: code=%s", j.Code)
		}
		if j.State == "done" {
			limits, err := st.GetPHPLimits(ctx, sid)
			if err != nil || limits.MemoryMB != 512 || string(j.Result) != "{}" {
				t.Fatalf("persisted result: %+v %+v %v", j, limits, err)
			}
			if _, err := os.Stat(filepath.Join(root, "sites-available", "demo.conf")); err != nil {
				t.Fatal(err)
			}
			pool, err := os.ReadFile(filepath.Join(root, "pool", "demo.conf"))
			if err != nil || !strings.Contains(string(pool), "memory_limit] = 512M") {
				t.Fatalf("pool: %s %v", pool, err)
			}
			op, err := st.LoadOperation(ctx, j.ID)
			if err != nil {
				t.Fatalf("audit operation: %+v %v", op, err)
			}
			if op.Identity.PublicationAudit == nil || op.Identity.PublicationAudit.State != "acknowledged" {
				select {
				case <-ctx.Done():
					t.Fatal("audit acknowledgement timed out")
				case <-time.After(10 * time.Millisecond):
				}
				continue
			}
			auditBytes, err := os.ReadFile(auditPath)
			if err != nil || !strings.Contains(string(auditBytes), op.Identity.PublicationAudit.IntentEventID) || !strings.Contains(string(auditBytes), op.Identity.PublicationAudit.FinalEventID) {
				t.Fatalf("audit evidence missing: %v", err)
			}
			return
		}
		select {
		case e := <-done:
			done <- e
			t.Fatalf("worker exited: %v", e)
		case <-ctx.Done():
			j, e := st.GetJob(context.Background(), enqueued.Job.ID)
			t.Fatalf("%v job=%+v lookup=%v", ctx.Err(), j, e)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

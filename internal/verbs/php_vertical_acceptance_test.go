package verbs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

// This exercises the dispatcher and the real queue, not a direct executor call.
// The scratch appliers and typed runner must be wired before this can pass.
func TestPHPSetQueuePublishesScratchConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store, a.Audit = st, log
	a.Nginx = &nginx.Applier{Root: root}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	if err := a.Jobs.PrepareOperations(ctx); err != nil {
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
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

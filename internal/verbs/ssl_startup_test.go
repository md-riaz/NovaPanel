package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTLSQueueStartupReopensOriginalACMEJob(t *testing.T) {
	for _, mode := range []string{"success", "running", "authority", "corrupt", "wrong-root", "stale-token", "wrong-job", "probe-error", "cancel"} {
		t.Run(mode, func(t *testing.T) { testTLSQueueRestart(t, mode) })
	}
}
func testTLSQueueRestart(t *testing.T, mode string) {
	ctx := context.Background()
	root := t.TempDir()
	db := filepath.Join(root, "panel.db")
	st, e := store.Open(db)
	if e != nil {
		t.Fatal(e)
	}
	log, e := audit.Open(st, filepath.Join(root, "audit.log"))
	if e != nil {
		t.Fatal(e)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Secret, _ = secret.New(make([]byte, 32))
	a.Cfg.TLSDir = filepath.Join(root, "tls")
	a.Cfg.ACMEWebroot = filepath.Join(root, "acme")
	a.Nginx.Root = filepath.Join(root, "nginx")
	for _, p := range []string{a.Cfg.TLSDir, filepath.Join(a.Cfg.ACMEWebroot, "demo"), filepath.Join(a.Nginx.Root, "sites-available"), filepath.Join(a.Nginx.Root, "sites-enabled")} {
		if e = os.MkdirAll(p, 0755); e != nil {
			t.Fatal(e)
		}
	}
	if e = os.Chmod(a.Cfg.TLSDir, 0700); e != nil {
		t.Fatal(e)
	}
	ca := newFakeCA(t, filepath.Join(a.Cfg.ACMEWebroot, "demo"))
	a.Cfg.ACMEDirectoryStaging = ca.server.URL + "/directory"
	a.Nginx.Runner = &acmeTestRunner{}
	a.Nginx.Probe = func(context.Context, nginx.Desired) error { panic("crash") }
	sid, _ := id.New()
	did, _ := id.New()
	if _, e = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: did, FQDN: "example.test"}); e != nil {
		t.Fatal(e)
	}
	if e = st.SetSiteStatus(ctx, sid, "active"); e != nil {
		t.Fatal(e)
	}
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	res := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "ssl.issue", Args: json.RawMessage(`{"site_id":"` + sid + `","staging":true}`)})
	if !res.OK {
		t.Fatal(res)
	}
	enq := res.Data.(jobs.EnqueueResult)
	if mode == "authority" {
		// Replace the unexecuted valid job with an original job carrying denied durable authority.
		old, _ := st.GetJob(ctx, enq.Job.ID)
		old.State = "failed"
		old.Code = "forbidden"
		if e = st.UpdateJob(ctx, old); e != nil {
			t.Fatal(e)
		}
		denied := jobs.WithMetadata(ctx, jobs.Metadata{Actor: "denied", Role: "read_only", CreatedBy: "denied", Effectful: true})
		enq, e = a.Jobs.Enqueue(denied, "ssl.issue", map[string]any{"site_id": sid, "staging": true}, "site:"+sid)
		if e != nil {
			t.Fatal(e)
		}
	}
	a.Jobs.SetRunner(func(ctx context.Context, j *jobs.Job) error {
		e := (acmeExecutor{a}).execute(ctx, j, "ssl.issue", ca.server.Client())
		return e
	})
	// Run the first queue until it has durably reported interruption.
	first, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(first) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, _ := st.GetJob(ctx, enq.Job.ID)
		if j.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first queue did not interrupt")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if mode == "running" {
		j, _ := st.GetJob(ctx, enq.Job.ID)
		j.State = "running"
		j.Code = ""
		j.Finished = nil
		if e = st.UpdateJob(ctx, j); e != nil {
			t.Fatal(e)
		}
	}
	if e = log.Close(); e != nil {
		t.Fatal(e)
	}
	if e = st.Close(); e != nil {
		t.Fatal(e)
	}
	st, e = store.Open(db)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	log, e = audit.Open(st, filepath.Join(root, "audit.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	a.Store = st
	a.Audit = log
	original, _ := st.TLSIntent(ctx)
	if len(original) == 0 {
		t.Fatal("missing interrupted journal")
	}
	if mode == "corrupt" || mode == "wrong-root" || mode == "stale-token" || mode == "wrong-job" {
		var in tlsIntent
		if e = json.Unmarshal(original, &in); e != nil {
			t.Fatal(e)
		}
		switch mode {
		case "wrong-root":
			in.Token.Root = filepath.Join(root, "untrusted")
		case "stale-token":
			in.Token.Version, _ = id.New()
		case "wrong-job":
			in.JobID, _ = id.New()
		}
		changed, _ := json.Marshal(in)
		if mode == "corrupt" {
			changed = []byte(`{"version":999}`)
		}
		if e = st.ClearTLSIntent(ctx, original); e != nil {
			t.Fatal(e)
		}
		if e = st.BeginTLSIntent(ctx, changed); e != nil {
			t.Fatal(e)
		}
	}
	a.Nginx.Probe = func(context.Context, nginx.Desired) error {
		if mode == "probe-error" {
			return errors.New("probe failed")
		}
		return nil
	}
	server = rpc.NewServer(a)
	server.ConfigureJobs()
	run, stop := context.WithCancel(ctx)
	defer stop()
	if mode == "cancel" {
		a.Nginx.Probe = func(context.Context, nginx.Desired) error { stop(); return context.Canceled }
	}
	done = make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	if mode != "success" && mode != "running" && mode != "probe-error" {
		select {
		case e = <-done:
			if mode != "cancel" && !errors.Is(e, jobs.ErrRecoveryBlocked) {
				t.Fatalf("expected blocked recovery: %v", e)
			}
		case <-time.After(5 * time.Second):
			stop()
			<-done
			t.Fatal("recovery did not stop")
		}
		j, _ := st.GetJob(ctx, enq.Job.ID)
		if j.State == "done" {
			t.Fatal("failed recovery completed")
		}
		intent, _ := st.TLSIntent(ctx)
		if mode != "probe-error" && mode != "cancel" && len(intent) == 0 {
			t.Fatal("unresolved journal lost")
		}
		ca.mu.Lock()
		defer ca.mu.Unlock()
		if ca.orders != 1 {
			t.Fatalf("CA orders=%d", ca.orders)
		}
		return
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		j, _ := st.GetJob(ctx, enq.Job.ID)
		if j.State == "done" || (mode == "probe-error" && j.State == "failed" && j.Code != "interrupted") {
			break
		}
		select {
		case e = <-done:
			t.Fatalf("restart stopped: %v", e)
		default:
		}
		if time.Now().After(deadline) {
			stop()
			<-done
			t.Fatalf("original job not completed: %+v", j)
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	intent, e := st.TLSIntent(ctx)
	if e != nil || len(intent) != 0 {
		t.Fatalf("intent retained %s %v", intent, e)
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if ca.orders != 1 {
		t.Fatalf("CA orders=%d", ca.orders)
	}
}

package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestQueuedServiceRestart(t *testing.T) {
	st, e := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	log, e := audit.Open(st, filepath.Join(t.TempDir(), "audit.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	seal, e := secret.New(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Secret = seal
	runner := &serviceRunner{}
	a.Exec = runner
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	r := s.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "service.restart", Args: json.RawMessage(`{"unit":"nginx.service"}`)})
	if !r.OK {
		t.Fatal(r)
	}
	en := r.Data.(jobs.EnqueueResult)
	if len(runner.calls) != 0 || !en.Job.Effectful || en.Job.ObjectKey != "service:nginx.service" {
		t.Fatalf("not queued %+v", en.Job)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(ctx) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	until := time.Now().Add(2 * time.Second)
	for {
		j, e := st.GetJob(context.Background(), en.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" {
			var result map[string]any
			if e = json.Unmarshal(j.Result, &result); e != nil {
				t.Fatal(e)
			}
			if result["unit"] != "nginx.service" || result["restarted_at"] == nil {
				t.Fatalf("result %s", j.Result)
			}
			break
		}
		if j.State == "failed" || time.Now().After(until) {
			t.Fatalf("job %+v", j)
		}
		time.Sleep(time.Millisecond)
	}
}

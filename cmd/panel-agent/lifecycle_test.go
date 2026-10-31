package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
	"novapanel/internal/validate"
)

type safeRunner struct{ ready chan struct{} }

func (r safeRunner) Run(_ context.Context, p string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/bin/chattr" && len(args) == 2 && args[0] == "+a" {
		return nil, nil, 0, nil
	}
	if p == "/usr/bin/systemctl" && len(args) == 2 && args[1] == "nginx.service" {
		switch args[0] {
		case "is-active":
			if r.ready != nil {
				r.ready <- struct{}{}
			}
			return []byte("active\n"), nil, 0, nil
		case "is-enabled":
			return []byte("enabled\n"), nil, 0, nil
		case "restart":
			return nil, nil, 0, nil
		}
	}
	return nil, nil, -1, errors.New("unexpected command")
}
func scratchAgent(t *testing.T) *agent.Agent {
	t.Helper()
	d := t.TempDir()
	c := config.Defaults()
	c.DBPath = filepath.Join(d, "state", "panel.db")
	c.SealKeyPath = filepath.Join(d, "state", "seal.key")
	c.LogDir = filepath.Join(d, "log")
	c.SocketPath = filepath.Join(d, "run", "agent.sock")
	c.ACMEWebroot = filepath.Join(d, "acme")
	c.WebUID = 65534
	c.WebGID = uint32(os.Getgid())
	return agent.NewWithDependencies(c, safeRunner{}, &validate.V{})
}
func waitAgent(t *testing.T, a *agent.Agent, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, e := net.Dial("unix", a.Cfg.SocketPath)
		if e == nil {
			c.Close()
			return
		}
		select {
		case e := <-done:
			t.Fatalf("startup failed: %v", e)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("startup timed out")
}
func TestProductionLifecycleScratch(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("socket ownership requires root in scratch")
	}
	a := scratchAgent(t)
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- serveScratch(ctx, a) }()
		waitAgent(t, a, done)
		c, e := rpc.Dial(a.Cfg.SocketPath)
		if e != nil {
			cancel()
			t.Fatal(e)
		}
		defer c.Close()
		r, e := c.Call(context.Background(), "service.status", map[string]string{"unit": "nginx.service"}, "")
		if e != nil || !r.OK {
			cancel()
			t.Fatalf("status: %+v %v", r, e)
		}
		r, e = c.Call(context.Background(), "service.restart", map[string]string{"unit": "nginx.service"}, "")
		if e != nil || !r.OK {
			cancel()
			t.Fatalf("restart: %+v %v", r, e)
		}
		var result struct {
			Job struct {
				ID string `json:"id"`
			} `json:"job"`
			Claim string `json:"claim"`
		}
		raw, _ := json.Marshal(r.Data)
		if e = json.Unmarshal(raw, &result); e != nil || result.Job.ID == "" {
			cancel()
			t.Fatalf("job: %s %v", raw, e)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			j, e := a.Store.GetJob(context.Background(), result.Job.ID)
			if e != nil {
				cancel()
				t.Fatal(e)
			}
			if j.State == "done" {
				if j.CreatedBy != "local-root" || j.Role != "owner" || j.Actor != "root@local" || !j.Effectful {
					t.Fatalf("metadata: %+v", j)
				}
				break
			}
			if j.State == "failed" || time.Now().After(deadline) {
				cancel()
				t.Fatalf("job: %+v", j)
			}
			time.Sleep(5 * time.Millisecond)
		}
		if e = a.Jobs.PublishClaim(context.Background(), result.Job.ID, map[string]any{"password": "scratch-secret"}); e != nil {
			cancel()
			t.Fatal(e)
		}
		claimed, e := c.Call(context.Background(), "job.claim", map[string]string{"job_id": result.Job.ID, "claim": result.Claim}, "")
		if e != nil || !claimed.OK {
			cancel()
			t.Fatalf("claim: %+v %v", claimed, e)
		}
		if _, e = c.Call(context.Background(), "job.claim", map[string]string{"job_id": result.Job.ID, "claim": result.Claim}, ""); e == nil {
			cancel()
			t.Fatal("claim reused")
		}
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown timed out")
		}
		for _, p := range []string{a.Cfg.SocketPath, a.Cfg.DBPath + ".pid"} {
			if _, e := os.Stat(p); !os.IsNotExist(e) {
				t.Fatalf("not removed: %s %v", p, e)
			}
		}
	}
}

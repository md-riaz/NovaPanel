package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"novapanel/internal/audit"
	"novapanel/internal/rpc"
)

func TestReadsSurviveStickyAuditFailure(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("scratch socket ownership requires root")
	}
	a := scratchAgent(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveScratch(ctx, a) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("shutdown timed out")
		}
	}()
	waitAgent(t, a, done)
	c, e := rpc.Dial(a.Cfg.SocketPath)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	reply, e := c.Call(ctx, "service.restart", map[string]string{"unit": "nginx.service"}, "")
	if e != nil {
		t.Fatal(e)
	}
	var result struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	raw, _ := json.Marshal(reply.Data)
	if e = json.Unmarshal(raw, &result); e != nil || result.Job.ID == "" {
		t.Fatalf("job: %s %v", raw, e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := a.Jobs.Status(ctx, result.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e = a.Audit.Record(audit.Event{Outcome: "invalid"}); e == nil {
		t.Fatal("expected sticky audit failure")
	}
	// Wait beyond the scheduler cadence so this checks daemon supervision too.
	select {
	case e := <-done:
		done <- e
		t.Fatalf("daemon exited on audit failure: %v", e)
	case <-time.After(1500 * time.Millisecond):
	}
	for _, request := range []struct {
		verb string
		args any
	}{{"ping", map[string]any{}}, {"job.status", map[string]string{"id": result.Job.ID}}, {"service.status", map[string]string{"unit": "nginx.service"}}} {
		if r, e := c.Call(ctx, request.verb, request.args, ""); e != nil || !r.OK {
			t.Fatalf("read %s: %+v %v", request.verb, r, e)
		}
	}
	for _, request := range []struct {
		verb string
		args any
	}{{"service.restart", map[string]string{"unit": "nginx.service"}}, {"schedule.set", map[string]any{"verb": "reconcile", "args": map[string]any{}, "cron": "minutely"}}} {
		if r, e := c.Call(ctx, request.verb, request.args, ""); e == nil || r.OK || r.Code != "agent_error" {
			t.Fatalf("mutation %s: %+v %v", request.verb, r, e)
		}
	}
}

package verbs_test

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

func TestPhaseOneQueueVerbs(t *testing.T) {
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
	a.Jobs = jobs.NewQueue(st, seal, log)
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	call := func(c rpc.Caller, verb, args string) rpc.Reply {
		return s.Dispatch(rpc.WithCaller(context.Background(), c), rpc.Request{Verb: verb, Args: json.RawMessage(args)})
	}
	root := rpc.RootCaller(0)
	r := call(root, "reconcile", `{}`)
	if !r.OK {
		t.Fatalf("enqueue: %+v", r)
	}
	b, _ := json.Marshal(r.Data)
	var en struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
		Claim string `json:"claim"`
	}
	if e = json.Unmarshal(b, &en); e != nil || en.Job.ID == "" {
		t.Fatalf("enqueue shape %s", b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	a.Jobs.SetRunner(s.ExecuteJob)
	go func() { done <- a.Jobs.Run(ctx) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	until := time.Now().Add(time.Second)
	for {
		j, e := st.GetJob(ctx, en.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" {
			if string(j.Result) != `{"changed":[]}` {
				t.Fatalf("result %s", j.Result)
			}
			break
		}
		if time.Now().After(until) {
			t.Fatalf("job %+v", j)
		}
		time.Sleep(time.Millisecond)
	}
	if e = a.Jobs.PublishClaim(ctx, en.Job.ID, map[string]any{"password": "once"}); e != nil {
		t.Fatal(e)
	}
	claimArgs := `{"job_id":"` + en.Job.ID + `","claim":"` + en.Claim + `"}`
	if r = call(root, "job.claim", claimArgs); !r.OK {
		t.Fatalf("claim %+v", r)
	}
	if r = call(root, "job.claim", claimArgs); r.Code != "not_found" {
		t.Fatalf("second claim %+v", r)
	}
	tokenClaim, e := a.Jobs.Enqueue(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "own-token", Role: "token", Scopes: []string{"sites:write"}}), "reconcile", map[string]any{}, "system")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Jobs.PublishClaim(ctx, tokenClaim.Job.ID, map[string]any{"password": "token-once"}); e != nil {
		t.Fatal(e)
	}
	tokenClaimArgs := `{"job_id":"` + tokenClaim.Job.ID + `","claim":"` + tokenClaim.ClaimToken + `"}`
	token := rpc.Caller{Kind: "token", Session: "own-token", Scopes: []string{"sites:write"}, Peer: 42}
	if r = call(root, "job.claim", tokenClaimArgs); r.OK {
		t.Fatal("foreign owner claimed secret")
	}
	if r = call(token, "job.claim", tokenClaimArgs); !r.OK {
		t.Fatalf("own claim scope exception %+v", r)
	}
	if r = call(token, "job.claim", tokenClaimArgs); r.Code != "not_found" {
		t.Fatal(r)
	}
	if r = call(root, "job.claim", `{"id":"`+en.Job.ID+`","claim":"x"}`); r.Code != "bad_request" {
		t.Fatalf("old claim shape accepted %+v", r)
	}
	if r = call(root, "job.list", `{}`); !r.OK {
		t.Fatal(r)
	} else {
		raw, _ := json.Marshal(r.Data)
		var envelope map[string]json.RawMessage
		_ = json.Unmarshal(raw, &envelope)
		if envelope["jobs"] == nil {
			t.Fatalf("list envelope %s", raw)
		}
	}
	if r = call(root, "schedule.set", `{"verb":"reconcile","args":{},"cron":"daily"}`); !r.OK {
		t.Fatalf("schedule %+v", r)
	}
	if r = call(root, "schedule.list", `{}`); !r.OK {
		t.Fatal(r)
	}
	token = rpc.Caller{Kind: "token", Session: "own-token", Scopes: []string{"sites:write"}, Peer: 42}
	j, e := st.GetJob(ctx, en.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	own, e := a.Jobs.Enqueue(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "own-token", Role: "admin"}), "reconcile", map[string]any{}, "system")
	if e != nil {
		t.Fatal(e)
	}
	_ = j
	if r = call(token, "job.status", `{"id":"`+own.Job.ID+`"}`); !r.OK {
		t.Fatalf("own status scope exception %+v", r)
	}
	if r = call(token, "job.status", `{"id":"`+en.Job.ID+`"}`); r.Code != "forbidden" {
		t.Fatalf("foreign status %+v", r)
	}
	if r = call(rpc.Caller{Role: "admin", Session: "admin-session", Peer: 42}, "schedule.set", `{"verb":"reconcile","args":{},"cron":"daily"}`); r.Code != "forbidden" {
		t.Fatalf("schedule authority escalation %+v", r)
	}
	r = call(root, "schedule.set", `{"verb":"reconcile","args":{},"cron":"daily","enabled":false}`)
	if !r.OK {
		t.Fatal(r)
	}
	scheduleID := r.Data.(map[string]string)["id"]
	if r = call(rpc.Caller{Role: "admin", Session: "admin-session", Peer: 42}, "schedule.enable", `{"id":"`+scheduleID+`"}`); r.Code != "forbidden" {
		t.Fatalf("enable authority escalation %+v", r)
	}
	if r = call(root, "schedule.enable", `{"id":"`+scheduleID+`"}`); !r.OK {
		t.Fatal(r)
	}
}

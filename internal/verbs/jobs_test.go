package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/config"
	run "novapanel/internal/exec"
	"novapanel/internal/rpc"
	"path/filepath"
	"testing"
	"time"
)

type safeRunner struct{ calls chan string }

func (r safeRunner) Run(_ context.Context, p string, args []string, _ run.RunOpts) ([]byte, []byte, int, error) {
	r.calls <- p + ":" + args[0]
	return []byte("active"), nil, 0, nil
}
func TestQueuedRestartAndScheduleWiring(t *testing.T) {
	d := t.TempDir()
	c := config.Defaults()
	c.DBPath = filepath.Join(d, "db")
	c.SealKeyPath = filepath.Join(d, "key")
	c.LogDir = filepath.Join(d, "logs")
	c.SocketPath = filepath.Join(d, "run", "agent.sock")
	c.ACMEWebroot = filepath.Join(d, "acme")
	runner := safeRunner{make(chan string, 20)}
	a := agent.NewWithDependencies(c, runner, nil)
	close, e := a.OpenState(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer close()
	s := rpc.NewServer(a)
	s.ConfigureJobs()
	ctx := rpc.WithCaller(context.Background(), rpc.RootCaller(0))
	dispatch := func(verb, args string) rpc.Reply {
		return s.Dispatch(ctx, rpc.Request{Verb: verb, Args: json.RawMessage(args)})
	}
	r := dispatch("service.restart", `{"unit":"nginx.service"}`)
	if !r.OK {
		t.Fatalf("enqueue: %+v", r)
	}
	b, _ := json.Marshal(r.Data)
	var result struct {
		Job struct {
			ID, CreatedBy, Role string
			Effectful           bool
		}
	}
	if e = json.Unmarshal(b, &result); e != nil {
		t.Fatal(e)
	}
	j, e := a.Jobs.Status(ctx, result.Job.ID)
	if e != nil || j.CreatedBy != "root@local" || j.Role != "owner" || !j.Effectful {
		t.Fatalf("metadata: %+v %v", j, e)
	}
	worker, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(worker) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		j, e = a.Jobs.Status(ctx, j.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" {
			break
		}
		if j.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("worker: %+v", j)
		}
		time.Sleep(time.Millisecond * 10)
	}
	if r = dispatch("schedule.set", `{"verb":"reconcile","cron":"minutely","args":{}}`); !r.OK {
		t.Fatalf("schedule: %+v", r)
	}
 rows,e:=a.Scheduler.List(ctx);if e!=nil||len(rows)!=1{t.Fatalf("schedules: %v %v",rows,e)}
 row:=rows[0];row.NextRun=a.Now()-1;if e=a.Store.PutSchedule(ctx,row);e!=nil{t.Fatal(e)}
 if e=a.Scheduler.Tick(ctx);e!=nil{t.Fatal(e)}
 list,e:=a.Jobs.List(ctx,store.JobFilter{Verb:"reconcile"});if e!=nil||len(list)!=1||list[0].CreatedBy!="root@local"{t.Fatalf("scheduled job: %v %v",list,e)}
 // Even persisted jobs must satisfy today's compiled-in authorization and args.
 changed:=j;changed.Role="read_only"
 trusted:=jobs.WithMetadata(context.Background(),jobs.Metadata{Actor:j.Actor,Role:"read_only",CreatedBy:j.CreatedBy,Effectful:true})
 if e=s.ExecuteJob(trusted,&changed);e==nil{t.Fatal("worker accepted read-only mutation")}
 changed=j;changed.Args=json.RawMessage(`{"unit":"ssh.service"}`)
 trusted=jobs.WithMetadata(context.Background(),jobs.Metadata{Actor:j.Actor,Role:j.Role,CreatedBy:j.CreatedBy,Effectful:true})
 if e=s.ExecuteJob(trusted,&changed);e==nil{t.Fatal("worker accepted unmanaged unit")}
 if e=s.ExecuteJob(context.Background(),&j);e==nil{t.Fatal("worker accepted untrusted context")}
 token:=rpc.WithCaller(context.Background(),rpc.Caller{Role:"token",Kind:"token",Scopes:[]string{"*"},UserID:"token"})
 if r=s.Dispatch(token,rpc.Request{Verb:"schedule.list",Args:json.RawMessage(`{}`)});r.Code!="forbidden"{t.Fatalf("token schedule policy: %+v",r)}
	if r = dispatch("schedule.set", `{"verb":"service.restart","cron":"minutely","args":{"unit":"nginx.service"}}`); r.Code != "invalid_argument" {
		t.Fatalf("schedule allowlist: %+v", r)
	}
	admin := rpc.WithCaller(context.Background(), rpc.Caller{Role: "admin", UserID: "admin", Actor: "admin"})
	if r = s.Dispatch(admin, rpc.Request{Verb: "schedule.list", Args: json.RawMessage(`{}`)}); r.Code != "forbidden" {
		t.Fatalf("owner policy: %+v", r)
	}
	ro := rpc.WithCaller(context.Background(), rpc.Caller{Role: "read_only", UserID: "reader"})
	if r = s.Dispatch(ro, rpc.Request{Verb: "reconcile", Args: json.RawMessage(`{}`)}); r.Code != "forbidden" {
		t.Fatalf("read-only policy: %+v", r)
	}
}

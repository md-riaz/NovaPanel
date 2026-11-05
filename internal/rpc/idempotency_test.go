package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestDispatchReplayBeforeResolution(t *testing.T) {
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
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Jobs = jobs.NewQueue(st, seal, log)
	s := NewServer(a)
	exists := true
	s.Registry = map[string]Verb{"test.slow": {Name: "test.slow", MinRole: Admin, Slow: true, NewArgs: func() any { return &struct{}{} }, ResolveObjectKey: func(context.Context, *agent.Agent, any) (string, error) {
		if !exists {
			return "", Errf("not_found", "gone")
		}
		return "object", nil
	}, Run: func(ctx context.Context, a *agent.Agent, v any) (any, error) {
		return a.Jobs.Enqueue(ctx, "test.slow", v, "object")
	}}}
	req := Request{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Verb: "test.slow", Args: json.RawMessage(`{}`), IdempotencyKey: "key"}
	ctx := WithCaller(context.Background(), RootCaller(0))
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "rpc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.WebUID = uint32(os.Getuid())
	done := make(chan error, 1)
	go func() { done <- s.ServeContext(serveCtx, listener) }()
	defer func() { cancel(); <-done }()
	conn, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	wire := func(request Request) Reply {
		t.Helper()
		b, e := json.Marshal(request)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = fmt.Fprintf(conn, "%s\n", b); e != nil {
			t.Fatal(e)
		}
		if !scanner.Scan() {
			t.Fatalf("wire read: %v", scanner.Err())
		}
		var r Reply
		if e = json.Unmarshal(scanner.Bytes(), &r); e != nil {
			t.Fatal(e)
		}
		return r
	}
	owner := "local-root"
	if os.Getuid() != 0 {
		owner = "wire-owner"
		s.Sessions = identityResolver{caller: Caller{Kind: "user", Role: "owner", Session: owner}}
		req.Session = "authenticated"
		ctx = WithCaller(context.Background(), Caller{Kind: "user", Role: "owner", Session: owner, Peer: uint32(os.Getuid())})
	}
	first := wire(req)
	if !first.OK {
		t.Fatalf("first: %+v", first)
	}
	exists = false
	second := wire(req)
	if !second.OK {
		t.Fatalf("replay: %+v", second)
	}
	var x, y jobs.EnqueueResult
	for _, pair := range []struct {
		data any
		out  *jobs.EnqueueResult
	}{{first.Data, &x}, {second.Data, &y}} {
		b, e := json.Marshal(pair.data)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(b, pair.out); e != nil {
			t.Fatal(e)
		}
	}
	if x.Job.ID != y.Job.ID || x.ClaimToken != y.ClaimToken {
		t.Fatal("different admission")
	}
	other := req
	other.Args = json.RawMessage(`{"different":true}`)
	if r := s.Dispatch(ctx, other); r.Code != "conflict" {
		t.Fatalf("different payload: %+v", r)
	}
	if err := a.Jobs.PublishClaim(ctx, x.Job.ID, map[string]any{"password": "secret"}); err != nil {
		t.Fatal(err)
	}
	claimCtx := jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: owner})
	if _, err := a.Jobs.Claim(claimCtx, x.Job.ID, x.ClaimToken); err != nil {
		t.Fatal(err)
	}
	if r := wire(req); !r.OK {
		t.Fatalf("consumed: %+v", r)
	} else {
		b, _ := json.Marshal(r.Data)
		var replay jobs.EnqueueResult
		if err := json.Unmarshal(b, &replay); err != nil || replay.Job.ID != x.Job.ID || replay.ClaimToken != "" {
			t.Fatalf("consumed replay: %s, %v", b, err)
		}
	}
}

func TestConcurrentDispatchIdempotency(t *testing.T) {
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
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Jobs = jobs.NewQueue(st, seal, log)
	s := NewServer(a)
	s.Registry = map[string]Verb{"test.slow": {Name: "test.slow", MinRole: Admin, Slow: true, NewArgs: func() any { return &struct{}{} }, Run: func(ctx context.Context, a *agent.Agent, v any) (any, error) {
		return a.Jobs.Enqueue(ctx, "test.slow", v, "object")
	}}}
	req := Request{Verb: "test.slow", Args: json.RawMessage(`{}`), IdempotencyKey: "same"}
	ctx := WithCaller(context.Background(), RootCaller(0))
	var wg sync.WaitGroup
	out := make(chan Reply, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out <- s.Dispatch(ctx, req) }()
	}
	wg.Wait()
	close(out)
	var id, token string
	for r := range out {
		if !r.OK {
			t.Fatalf("dispatch: %+v", r)
		}
		v := r.Data.(jobs.EnqueueResult)
		if id == "" {
			id, token = v.Job.ID, v.ClaimToken
		}
		if id != v.Job.ID || token != v.ClaimToken {
			t.Fatal("duplicate admission")
		}
	}
	list, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("jobs=%d err=%v", len(list), err)
	}
}

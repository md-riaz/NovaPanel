package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/firewall"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
	"time"
)

type firewallPending struct{ p *firewall.Pending }

func (s *firewallPending) WithPending(_ context.Context, id string, fn func(firewall.Pending) (bool, error)) error {
	if s.p == nil || s.p.ID != id {
		return rpc.Errf("not_found", "missing")
	}
	consume, err := fn(*s.p)
	if err == nil && consume {
		s.p = nil
	}
	return err
}

type firewallSnapshot struct{ calls int }

func (s *firewallSnapshot) RestoreAndMark(context.Context, firewall.Pending) error {
	s.calls++
	return nil
}
func TestFirewallDispatchAuthorizationAndSynchronousRecovery(t *testing.T) {
	for _, verb := range []string{"firewall.confirm", "firewall.revert"} {
		for _, tc := range []struct {
			name    string
			caller  rpc.Caller
			allowed bool
		}{
			{"admin MFA", rpc.Caller{Kind: "user", Role: "admin", UserID: "admin", MFAat: 2000000}, true},
			{"admin no MFA", rpc.Caller{Kind: "user", Role: "admin", UserID: "admin"}, false},
			{"reader", rpc.Caller{Kind: "user", Role: "read_only", UserID: "reader", MFAat: 2000000}, false},
			{"automation", rpc.Caller{Kind: "token", Scopes: []string{"firewall:write"}, Automation: true}, true},
			{"ordinary token", rpc.Caller{Kind: "token", Scopes: []string{"firewall:write"}}, false},
			{"wrong scope", rpc.Caller{Kind: "token", Scopes: []string{"system:write"}, Automation: true}, false},
		} {
			t.Run(verb+tc.name, func(t *testing.T) {
				a := agent.New()
				a.Clock = firewallClock{}
				p := firewall.Pending{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SnapshotPath: "/scratch", RevertUnit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: time.UnixMilli(2060000)}
				pending := &firewallPending{p: &p}
				run := &serviceRunner{}
				snap := &firewallSnapshot{}
				a.FirewallRecovery = &firewall.Recovery{Store: pending, Runner: run, Snapshot: snap, Now: func() time.Time { return time.UnixMilli(a.Now()) }}
				server := rpc.NewServer(a)
				req := rpc.Request{Verb: verb, Args: json.RawMessage(`{"pending_id":"` + p.ID + `"}`)}
				got := server.Dispatch(rpc.WithCaller(context.Background(), tc.caller), req)
				if !tc.allowed {
					if got.Code != "forbidden" || pending.p == nil || len(run.calls)+snap.calls != 0 {
						t.Fatalf("denial %+v", got)
					}
					return
				}
				if verb == "firewall.confirm" {
					if got.Code != "unsupported" || pending.p == nil || len(run.calls)+snap.calls != 0 {
						t.Fatalf("unsafe confirmation %+v", got)
					}
					return
				}
				if !got.OK || pending.p != nil || len(run.calls)+snap.calls != 1 {
					t.Fatalf("recovery %+v", got)
				}
				if got := server.Dispatch(rpc.WithCaller(context.Background(), tc.caller), req); got.Code != "not_found" {
					t.Fatalf("replay %+v", got)
				}
			})
		}
	}
}
func TestFirewallDispatchDurableAdapter(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := firewall.Pending{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SnapshotPath: t.TempDir(), RevertUnit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: time.UnixMilli(2060000)}
	if err := s.CreateFirewallPending(ctx, p); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = s
	a.Clock = firewallClock{}
	run := &serviceRunner{}
	snap := &firewallSnapshot{}
	a.FirewallRecovery = &firewall.Recovery{Store: s, Runner: run, Snapshot: snap, Now: func() time.Time { return time.UnixMilli(a.Now()) }}
	server := rpc.NewServer(a)
	req := rpc.Request{Verb: "firewall.revert", Args: json.RawMessage(`{"pending_id":"` + p.ID + `"}`)}
	ctx = rpc.WithCaller(ctx, rpc.Caller{Kind: "token", Scopes: []string{"firewall:write"}, Automation: true})
	confirm := req
	confirm.Verb = "firewall.confirm"
	if got := server.Dispatch(ctx, confirm); got.Code != "unsupported" {
		t.Fatalf("unsafe durable confirmation %+v", got)
	}
	if len(run.calls) != 0 || snap.calls != 0 {
		t.Fatal("unsupported confirmation performed side effects")
	}
	if got := server.Dispatch(ctx, req); !got.OK {
		t.Fatalf("revert %+v", got)
	}
	if got := server.Dispatch(ctx, req); got.Code != "not_found" {
		t.Fatalf("replay %+v", got)
	}
	if len(run.calls) != 0 || snap.calls != 1 {
		t.Fatal(run.calls, snap.calls)
	}
}

type firewallClock struct{}

func (firewallClock) Now() int64 { return 2000000 }
func TestFirewallApplyDispatchAuthorization(t *testing.T) {
	a := agent.New()
	a.Clock = firewallClock{}
	a.Jobs = &jobs.Queue{}
	run := &serviceRunner{}
	a.Exec = run
	server := rpc.NewServer(a)
	for _, tc := range []struct {
		caller rpc.Caller
		code   string
	}{
		{rpc.Caller{Kind: "user", Role: "admin", UserID: "admin", MFAat: 2000000}, "unsupported"},
		{rpc.Caller{Kind: "user", Role: "admin", UserID: "admin"}, "forbidden"},
		{rpc.Caller{Kind: "token", Scopes: []string{"firewall:write"}, Automation: true}, "unsupported"},
		{rpc.Caller{Kind: "token", Scopes: []string{"firewall:write"}}, "forbidden"},
		{rpc.Caller{Kind: "token", Scopes: []string{"system:write"}, Automation: true}, "forbidden"},
	} {
		if got := server.Dispatch(rpc.WithCaller(context.Background(), tc.caller), rpc.Request{Verb: "firewall.apply", Args: json.RawMessage(`{"rules":[]}`)}); got.Code != tc.code {
			t.Fatalf("%+v %+v", tc.caller, got)
		}
	}
	if len(run.calls) != 0 {
		t.Fatal(run.calls)
	}
	if err := rpc.Registry["firewall.apply"].Exec(context.Background(), a, &jobs.Job{}); err == nil {
		t.Fatal("worker apply bypass")
	}
	a.Jobs = nil
	for _, verb := range []string{"firewall.confirm", "firewall.revert"} {
		if got := server.Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Kind: "token", Scopes: []string{"firewall:write"}, Automation: true}), rpc.Request{Verb: verb, Args: json.RawMessage(`{"pending_id":"01ARZ3NDEKTSV4RRFFQ69G5FAV"}`)}); got.Code != "unsupported" {
			t.Fatalf("nil adapter %+v", got)
		}
	}
}
func TestFirewallApplyFailsClosedWithoutRunnerCalls(t *testing.T) {
	a := agent.New()
	a.Clock = firewallClock{}
	a.Jobs = &jobs.Queue{}
	run := &serviceRunner{}
	a.Exec = run
	server := rpc.NewServer(a)
	ctx := rpc.WithCaller(context.Background(), rpc.Caller{Kind: "token", Scopes: []string{"firewall:write"}, Automation: true})
	for _, tc := range []struct{ args, code string }{
		{`{"rules":[]}`, "unsupported"},
		{`{"rules":[],"default_incoming":"allow"}`, "bad_request"},
		{`{"rules":[{"action":"allow;id","proto":"tcp","port":80}]}`, "invalid_argument"},
		{`{"rules":[],"window_min":-1}`, "invalid_argument"},
	} {
		if got := server.Dispatch(ctx, rpc.Request{Verb: "firewall.apply", Args: json.RawMessage(tc.args)}); got.Code != tc.code {
			t.Fatalf("%s %+v", tc.args, got)
		}
	}
	if len(run.calls) != 0 {
		t.Fatal(run.calls)
	}
	v := rpc.Registry["firewall.apply"]
	if !v.Slow || !v.Effectful || !v.MFA || v.Scope != "firewall:write" || v.ObjectKey(v.NewArgs()) != "firewall" {
		t.Fatal("incorrect apply metadata")
	}
}

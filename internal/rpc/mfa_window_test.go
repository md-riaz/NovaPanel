package rpc

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"testing"
)

type authClock int64

func (c authClock) Now() int64 { return int64(c) }

func TestMFAGateUsesConfiguredWindowAndRejectsFutureProof(t *testing.T) {
	a := agent.New()
	a.Clock = authClock(1000000)
	a.Cfg.ReauthWindowSec = 10
	s := NewServer(a)
	s.Registry = map[string]Verb{"write": {Name: "write", MinRole: Admin, MFA: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { return struct{}{}, nil }}}
	req := Request{Verb: "write", Args: json.RawMessage(`{}`)}
	for _, tc := range []struct {
		name string
		at   int64
		ok   bool
	}{
		{"boundary", 990000, true}, {"expired", 989999, false}, {"future", 1000001, false}, {"missing", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Dispatch(WithCaller(context.Background(), Caller{Kind: "user", Role: "admin", Session: "session", UserID: "user", MFAat: tc.at, Peer: 1001}), req)
			if got.OK != tc.ok {
				t.Fatalf("unexpected MFA decision: %+v", got)
			}
			if !tc.ok && (got.Code != "forbidden" || got.Data.(map[string]bool)["reauth_required"] != true) {
				t.Fatalf("missing step-up hint: %+v", got)
			}
		})
	}
}

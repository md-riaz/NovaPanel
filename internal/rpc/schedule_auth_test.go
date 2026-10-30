package rpc

import (
	"context"
	"novapanel/internal/agent"
	"testing"
)

func TestScheduleTargetAuthorization(t *testing.T) {
	old := Registry
	defer func() { Registry = old }()
	Registry = map[string]Verb{"target": {MinRole: Admin, Scope: "system:write", MFA: true}}
	for _, tt := range []struct {
		name    string
		c       Caller
		allowed bool
	}{
		{"admin no automation", Caller{Kind: "user", Role: "admin"}, false},
		{"admin automation", Caller{Kind: "user", Role: "admin", Automation: true}, true},
		{"readonly automation", Caller{Kind: "user", Role: "read_only", Automation: true}, false},
		{"token scope", Caller{Kind: "token", Scopes: []string{"system:write"}, Automation: true}, true},
		{"token wrong scope", Caller{Kind: "token", Scopes: []string{"system:read"}, Automation: true}, false},
		{"token no scope owner label", Caller{Kind: "token", Role: "owner", Automation: true}, false},
		{"token no grant", Caller{Kind: "token", Scopes: []string{"*"}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := AuthorizeSchedule(WithCaller(context.Background(), tt.c), "target")
			if (e == nil) != tt.allowed {
				t.Fatalf("allowed=%v err=%v", tt.allowed, e)
			}
			if tt.name == "admin no automation" {
				r := errorReply(e)
				if r.Code != "forbidden" || r.Data == nil {
					t.Fatalf("missing reauth hint %+v", r)
				}
			}
		})
	}
	v := Registry["target"]
	v.ReadOnly = true
	v.MFA = false
	Registry["target"] = v
	if e := AuthorizeSchedule(WithCaller(context.Background(), Caller{Role: "read_only"}), "target"); e != nil {
		t.Fatal("ReadOnly override", e)
	}
}

func TestDispatchAutomationMFA(t *testing.T) {
	a := agent.New()
	s := NewServer(a)
	s.Registry = map[string]Verb{"target": {Scope: "system:write", MFA: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { return struct{}{}, nil }}}
	r := s.Dispatch(WithCaller(context.Background(), Caller{Kind: "token", Session: "token", Scopes: []string{"system:write"}, Automation: true}), Request{Verb: "target", Args: []byte(`{}`)})
	if !r.OK {
		t.Fatalf("automation denied %+v", r)
	}
}

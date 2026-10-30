package rpc

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"testing"
)

func TestRegistryRoleScopeMatrix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		min     Role
		read    bool
		caller  Caller
		allowed bool
	}{
		{"readonly marked owner read", Owner, true, Caller{Role: "read_only"}, true},
		{"admin below owner floor", Owner, true, Caller{Role: "admin"}, false},
		{"readonly unmarked read", ReadOnly, false, Caller{Role: "read_only"}, false},
		{"admin schedule write", Admin, false, Caller{Role: "admin"}, true},
		{"owner schedule write", Admin, false, Caller{Role: "owner"}, true},
		{"readonly schedule write", Admin, false, Caller{Role: "read_only"}, false},
		{"token matching scope", Owner, false, Caller{Kind: "token", Scopes: []string{"system:read"}}, true},
		{"token no scope", ReadOnly, true, Caller{Kind: "token"}, false},
		{"anonymous marked read", Admin, true, Caller{Kind: "anon"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(agent.New())
			s.Registry = map[string]Verb{"schedule.test": {Name: "schedule.test", MinRole: tc.min, ReadOnly: tc.read, Scope: "system:read", NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { return struct{}{}, nil }}}
			r := s.Dispatch(WithCaller(context.Background(), tc.caller), Request{Verb: "schedule.test", Args: json.RawMessage(`{}`)})
			if r.OK != tc.allowed {
				t.Fatalf("allowed=%v, reply=%+v", tc.allowed, r)
			}
		})
	}
}

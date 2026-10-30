package rpc

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"testing"
)

func TestDispatchMetadataFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name            string
		scope, module   string
		slow, effectful bool
		caller          Caller
		want            string
	}{
		{name: "module unsupported", module: "dns", caller: RootCaller(0), want: "unsupported"},
		{name: "slow unavailable", slow: true, caller: RootCaller(0), want: "unsupported"},
		{name: "effectful unavailable", effectful: true, caller: RootCaller(0), want: "unsupported"},
		{name: "token no scope", caller: Caller{Role: "token", Scopes: []string{"*"}}, want: "forbidden"},
		{name: "token missing grant", scope: "info:read", caller: Caller{Role: "token"}, want: "forbidden"},
		{name: "token wrong grant", scope: "info:read", caller: Caller{Role: "token", Scopes: []string{"other"}}, want: "forbidden"},
		{name: "token scoped", scope: "info:read", caller: Caller{Role: "token", Scopes: []string{"info:read"}}},
		{name: "token wildcard", scope: "info:read", caller: Caller{Role: "token", Scopes: []string{"*"}}},
		{name: "token kind cannot use owner role", caller: Caller{Kind: "token", Role: "owner", Scopes: []string{"*"}}, want: "forbidden"},
		{name: "token kind scoped", scope: "info:read", caller: Caller{Kind: "token", Scopes: []string{"info:read"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			s := NewServer(agent.New())
			s.Registry = map[string]Verb{"test": {Name: "test", Public: true, Scope: tc.scope, Module: tc.module, Slow: tc.slow, Effectful: tc.effectful, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { called = true; return nil, nil }}}
			r := s.Dispatch(WithCaller(context.Background(), tc.caller), Request{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Verb: "test", Args: json.RawMessage(`{}`)})
			if r.Code != tc.want || called != (tc.want == "") {
				t.Fatalf("reply=%+v called=%v", r, called)
			}
		})
	}
}

package rpc

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"testing"
)

func TestSelfServiceRequiresOwnUserSession(t *testing.T) {
	for _, name := range []string{"session.reauth", "session.logout", "mfa.enroll", "mfa.confirm", "mfa.disable"} {
		t.Run(name, func(t *testing.T) {
			a := agent.New()
			s := NewServer(a)
			runs := 0
			s.Registry = map[string]Verb{name: {Name: name, MinRole: ReadOnly, NewArgs: func() any { return &struct{}{} }, Run: func(ctx context.Context, _ *agent.Agent, _ any) (any, error) {
				c := CallerFrom(ctx)
				if c.UserID != "own-user" {
					t.Fatalf("wrong subject: %+v", c)
				}
				runs++
				return struct{}{}, nil
			}}}
			req := Request{Verb: name, Args: json.RawMessage(`{}`)}
			own := Caller{Kind: "user", Role: "read_only", UserID: "own-user", Session: "own-session", Peer: 1001}
			if got := s.Dispatch(WithCaller(context.Background(), own), req); !got.OK {
				t.Fatalf("own lifecycle refused: %+v", got)
			}
			req.Args = json.RawMessage(`{"user_id":"other-user"}`)
			if got := s.Dispatch(WithCaller(context.Background(), own), req); got.Code != "bad_request" {
				t.Fatalf("target accepted: %+v", got)
			}
			req.Args = json.RawMessage(`{}`)
			for _, c := range []Caller{
				{Kind: "token", Role: "owner", Scopes: []string{"*"}, UserID: "own-user", Peer: 1001},
				{Kind: "anon", Role: "owner", Peer: 1001},
				{Kind: "user", Role: "read_only", Peer: 1001},
				RootCaller(0),
			} {
				if got := s.Dispatch(WithCaller(context.Background(), c), req); got.OK {
					t.Fatalf("non-user accepted: %+v", c)
				}
			}
			if runs != 1 {
				t.Fatalf("ran %d times", runs)
			}
		})
	}
}

func TestAnonymousCannotReachOtherPublicVerbs(t *testing.T) {
	s := NewServer(agent.New())
	s.Registry = map[string]Verb{"ping": {Name: "ping", Public: true, ReadOnly: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { return struct{}{}, nil }}}
	req := Request{Verb: "ping", Args: json.RawMessage(`{}`)}
	got := s.Dispatch(WithCaller(context.Background(), Caller{Kind: "anon", Peer: 1001}), req)
	if got.OK {
		t.Fatal("anon reached ping outside explicit pre-login allowlist")
	}
	if got := s.Dispatch(WithCaller(context.Background(), Caller{Peer: 1001}), req); !got.OK {
		t.Fatalf("sessionless public ping denied: %+v", got)
	}
}

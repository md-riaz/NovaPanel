package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"testing"
)

func TestSelfServiceRemainsAuditedMutation(t *testing.T) {
	a := agent.New()
	s := NewServer(a)
	ran := false
	s.Registry = map[string]Verb{"session.logout": {Name: "session.logout", MinRole: ReadOnly, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { ran = true; return struct{}{}, nil }}}
	var events []AuditEvent
	s.DurableAudit = func(e AuditEvent) error { events = append(events, e); return errors.New("sink unavailable") }
	got := s.Dispatch(WithCaller(context.Background(), Caller{Kind: "user", Role: "read_only", UserID: "own", Session: "sid", Peer: 1001}), Request{Verb: "session.logout", Args: json.RawMessage(`{}`)})
	if ran || got.Code != "agent_error" || len(events) != 2 {
		t.Fatalf("audit bypass: ran=%v reply=%+v events=%d", ran, got, len(events))
	}
}

func TestSelfServiceExceptionDoesNotPermitOtherWrites(t *testing.T) {
	s := NewServer(agent.New())
	s.Registry = map[string]Verb{"other.write": {Name: "other.write", MinRole: ReadOnly, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { t.Fatal("ordinary write ran"); return nil, nil }}}
	got := s.Dispatch(WithCaller(context.Background(), Caller{Kind: "user", Role: "read_only", UserID: "own", Session: "sid", Peer: 1001}), Request{Verb: "other.write", Args: json.RawMessage(`{}`)})
	if got.Code != "forbidden" {
		t.Fatalf("write exemption widened: %+v", got)
	}
}

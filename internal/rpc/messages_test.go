package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"testing"
	"time"
)

type checkedArgs struct {
	Value string `json:"value"`
}

func (a *checkedArgs) Validate() error {
	if a.Value == "bad" {
		return Errf("invalid_argument", "bad value")
	}
	return nil
}
func TestDispatchAuthorityAndTypedArgs(t *testing.T) {
	a := agent.New()
	s := NewServer(a)
	s.WebUID = 1001
	s.Registry = map[string]Verb{"owner": {Name: "owner", MinRole: Owner, ReadOnly: true, NewArgs: func() any { return &checkedArgs{} }, Run: func(ctx context.Context, _ *agent.Agent, args any) (any, error) {
		return args.(*checkedArgs).Value, nil
	}}}
	req := Request{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Verb: "owner", Args: json.RawMessage(`{"value":"yes"}`), Actor: "owner"}
	for _, caller := range []Caller{{Peer: 1001}, {Peer: 1001, Role: "admin"}} {
		got := s.Dispatch(WithCaller(context.Background(), caller), req)
		if got.OK || got.ID != req.ID {
			t.Fatalf("authority bypass: %+v", got)
		}
	}
	ctx := WithCaller(context.Background(), RootCaller(0))
	if got := s.Dispatch(ctx, req); !got.OK || got.Data != "yes" {
		t.Fatalf("owner rejected %+v", got)
	}
	req.Args = json.RawMessage(`{"value":"bad"}`)
	if got := s.Dispatch(ctx, req); got.Code != "invalid_argument" {
		t.Fatalf("validation %+v", got)
	}
	req.Session = "unknown"
	if got := s.Dispatch(WithCaller(context.Background(), Caller{Peer: 1001}), req); got.Code != "unauthenticated" {
		t.Fatalf("session %+v", got)
	}
}
func TestDispatchDeadlinesErrorsAndAudit(t *testing.T) {
	s := NewServer(agent.New())
	var events []AuditEvent
	s.Audit = AuditFunc(func(ev AuditEvent) { events = append(events, ev) })
	s.Registry = map[string]Verb{"test": {Name: "test", Public: true, MaxDeadlineMs: 10, NewArgs: func() any { return &struct{}{} }, Run: func(ctx context.Context, _ *agent.Agent, _ any) (any, error) { <-ctx.Done(); return nil, ctx.Err() }}}
	req := Request{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Verb: "test", Actor: "audit-only", Args: json.RawMessage(`{}`), DeadlineMs: 99999}
	start := time.Now()
	got := s.Dispatch(context.Background(), req)
	if got.Code != "deadline_exceeded" || time.Since(start) > time.Second {
		t.Fatalf("deadline: %+v", got)
	}
	v := s.Registry["test"]
	v.Run = func(context.Context, *agent.Agent, any) (any, error) { return nil, errors.New("secret details") }
	s.Registry["test"] = v
	if got := s.Dispatch(context.Background(), req); got.Code != "agent_error" || got.Error == "secret details" {
		t.Fatalf("leak %+v", got)
	}
	v.Run = func(context.Context, *agent.Agent, any) (any, error) { panic("secret") }
	s.Registry["test"] = v
	if got := s.Dispatch(context.Background(), req); got.Code != "agent_error" {
		t.Fatalf("panic %+v", got)
	}
	if len(events) != 3 || events[0].Actor != "audit-only" {
		t.Fatalf("audit %+v", events)
	}
}

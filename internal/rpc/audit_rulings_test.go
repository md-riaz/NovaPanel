package rpc

import (
	"context"
	"errors"
	"novapanel/internal/agent"
	"testing"
)

func TestAuditFailureGatesMutationButNotReads(t *testing.T) {
	for _, read := range []bool{false, true} {
		s := NewServer(agent.New())
		called := false
		s.Registry = map[string]Verb{"test": {Name: "test", Public: true, ReadOnly: read, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { called = true; return struct{}{}, nil }}}
		s.DurableAudit = func(AuditEvent) error { return errors.New("unavailable") }
		r := s.Dispatch(context.Background(), Request{Verb: "test", Args: []byte(`{}`)})
		if read {
			if !called || !r.OK {
				t.Fatalf("read stopped: %+v", r)
			}
		} else if called || r.Code != "agent_error" {
			t.Fatalf("mutation ran: %+v", r)
		}
	}
}
func TestRefusalAuditStage(t *testing.T) {
	s := NewServer(agent.New())
	var events []AuditEvent
	s.DurableAudit = func(e AuditEvent) error { events = append(events, e); return nil }
	s.Registry = map[string]Verb{"test": {Name: "test", MinRole: Owner, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) {
		t.Fatal("unauthenticated action")
		return nil, nil
	}}}
	r := s.Dispatch(context.Background(), Request{Verb: "test", Actor: "untrusted", Args: []byte(`{}`)})
	if r.Code != "unauthenticated" || len(events) != 1 || events[0].Outcome != "refused" || events[0].Detail["stage"] != "authz" {
		t.Fatalf("reply=%+v events=%+v", r, events)
	}
}

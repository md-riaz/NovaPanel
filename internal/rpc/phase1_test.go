package rpc

import (
	"context"
	"errors"
	"novapanel/internal/agent"
	"testing"
)

func TestMutationFailsBeforeActionOnAuditFailure(t *testing.T) {
	s := NewServer(agent.New())
	called := false
	s.Registry = map[string]Verb{"mutate": {Name: "mutate", Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { called = true; return nil, nil }}}
	s.DurableAudit = func(AuditEvent) error { return errors.New("unavailable") }
	r := s.Dispatch(context.Background(), Request{Verb: "mutate", Args: []byte(`{}`)})
	if called || r.Code != "agent_error" {
		t.Fatalf("called=%v reply=%+v", called, r)
	}
}

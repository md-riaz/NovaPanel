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
func TestCompletionAuditFailureOnlyPreservesSuccessfulClaim(t *testing.T) {
	for _, tc := range []struct {
		name       string
		handlerErr error
	}{
		{"other.mutation", nil},
		{"other.mutation.error", Errf("not_found", "missing")},
		{"job.claim", Errf("not_found", "missing")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(agent.New())
			calls, ran := 0, false
			s.Registry = map[string]Verb{tc.name: {Name: tc.name, Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) {
				ran = true
				return nil, tc.handlerErr
			}}}
			s.DurableAudit = func(AuditEvent) error {
				calls++
				if calls == 2 {
					return errors.New("completion failure")
				}
				return nil
			}
			r := s.Dispatch(context.Background(), Request{ID: "error-reply", Verb: tc.name, Args: []byte(`{}`)})
			if !ran || calls != 2 || r.OK || r.Code != "agent_error" || r.ID != "error-reply" || r.Data != nil {
				t.Fatalf("completion gating changed: %+v calls=%d ran=%v", r, calls, ran)
			}
		})
	}
}

func TestClaimPreExecutionAuditFailureDoesNotRun(t *testing.T) {
	s := NewServer(agent.New())
	s.Registry = map[string]Verb{"job.claim": {Name: "job.claim", Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) {
		t.Fatal("claim ran before healthy audit")
		return nil, nil
	}}}
	s.DurableAudit = func(AuditEvent) error { return errors.New("unavailable") }
	r := s.Dispatch(context.Background(), Request{Verb: "job.claim", Args: []byte(`{}`)})
	if r.OK || r.Code != "agent_error" {
		t.Fatalf("pre-execution gate bypassed: %+v", r)
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

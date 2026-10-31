package rpc

import (
	"novapanel/internal/agent"
	"sync/atomic"
	"testing"
)

type attemptClock struct{ now atomic.Int64 }

func (c *attemptClock) Now() int64 { return c.now.Load() }

func TestBootstrapAdmissionWindow(t *testing.T) {
	s := NewServer(agent.New())
	c := &attemptClock{}
	c.now.Store(1000)
	s.Agent.Clock = c
	for i := 0; i < 100; i++ {
		if !s.allowAuthentication(1001, "session.start", nil) {
			t.Fatalf("visitor %d refused", i+1)
		}
	}
	if s.allowAuthentication(1001, "session.start", nil) {
		t.Fatal("unbounded mint")
	}
	if !s.allowAuthentication(1002, "session.start", nil) {
		t.Fatal("peer isolation")
	}
	for _, verb := range []string{"session.login", "session.reauth", "mfa.confirm", "mfa.disable"} {
		if !s.allowAuthentication(1001, verb, nil) {
			t.Fatal("credential verification must use account budgets")
		}
	}
	c.now.Add(49)
	if s.allowAuthentication(1001, "session.start", nil) {
		t.Fatal("early refill")
	}
	c.now.Add(1)
	if !s.allowAuthentication(1001, "session.start", nil) || s.allowAuthentication(1001, "session.start", nil) {
		t.Fatal("20 rps refill")
	}
	c.now.Add(-1000)
	if s.allowAuthentication(1001, "session.start", nil) {
		t.Fatal("clock rollback restored allowance")
	}
}

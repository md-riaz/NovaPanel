package rpc

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"sync"
	"sync/atomic"
	"testing"
)

type attemptClock struct{ now atomic.Int64 }

func (c *attemptClock) Now() int64 { return c.now.Load() }

func attemptHarness() (*Server, *attemptClock, func(string, string, string) Reply) {
	a := agent.New()
	c := &attemptClock{}
	c.now.Store(1800000000000)
	a.Clock = c
	a.Cfg.LoginMaxAttempts = 2
	a.Cfg.LoginWindowSec = 17
	a.Cfg.TOTPMaxAttempts = 1
	s := NewServer(a)
	s.Registry = map[string]Verb{}
	for _, name := range []string{"session.start", "session.login", "session.reauth", "mfa.enroll", "mfa.confirm", "mfa.disable"} {
		s.Registry[name] = Verb{Name: name, Public: true, NewArgs: func() any {
			return &struct {
				TOTP string `json:"totp"`
			}{}
		}, Run: func(context.Context, *agent.Agent, any) (any, error) {
			return nil, Errf("unauthenticated", "bad proof")
		}}
	}
	return s, c, func(verb, actor, session string) Reply {
		caller := Caller{Peer: 1001, Kind: "user", Role: "read_only", UserID: "user", Session: "session"}
		return s.Dispatch(WithCaller(context.Background(), caller), Request{Verb: verb, Actor: actor, Session: session, Args: json.RawMessage(`{}`)})
	}
}

func TestConfiguredAttemptWindowBoundary(t *testing.T) {
	_, c, call := attemptHarness()
	for i := 0; i < 2; i++ {
		if r := call("session.login", "", ""); r.Code != "unauthenticated" {
			t.Fatalf("attempt %d: %+v", i, r)
		}
	}
	c.now.Add(16999)
	if r := call("session.login", "", ""); r.Code != "forbidden" {
		t.Fatalf("before boundary: %+v", r)
	}
	c.now.Add(1)
	if r := call("session.login", "", ""); r.Code != "unauthenticated" {
		t.Fatalf("at boundary: %+v", r)
	}
}

func TestBootstrapDoesNotSpendCredentialBudgets(t *testing.T) {
	_, _, call := attemptHarness()
	for i := 0; i < 10; i++ {
		if r := call("session.start", "", ""); r.Code != "unauthenticated" {
			t.Fatalf("bootstrap %d: %+v", i, r)
		}
	}
	if r := call("session.start", "", ""); r.Code != "forbidden" {
		t.Fatalf("bootstrap unbounded: %+v", r)
	}
	if r := call("session.login", "", ""); r.Code != "unauthenticated" {
		t.Fatalf("bootstrap spent login: %+v", r)
	}
	if r := call("mfa.confirm", "", ""); r.Code != "unauthenticated" {
		t.Fatalf("bootstrap spent MFA: %+v", r)
	}
}

func TestMFAProofsShareBudgetWithoutEnrollmentReset(t *testing.T) {
	for _, first := range []string{"session.reauth", "mfa.confirm", "mfa.disable"} {
		t.Run(first, func(t *testing.T) {
			_, c, call := attemptHarness()
			if r := call(first, "", ""); r.Code != "unauthenticated" {
				t.Fatalf("first: %+v", r)
			}
			for i := 0; i < 3; i++ {
				if r := call("mfa.enroll", "", ""); r.Code != "unauthenticated" {
					t.Fatalf("enroll charged proof: %+v", r)
				}
			}
			for _, next := range []string{"session.reauth", "mfa.confirm", "mfa.disable"} {
				if r := call(next, "forged actor", "forged session"); r.Code != "forbidden" {
					t.Fatalf("%s bypass: %+v", next, r)
				}
			}
			c.now.Add(17000)
			if r := call("mfa.confirm", "", ""); r.Code != "unauthenticated" {
				t.Fatalf("MFA window: %+v", r)
			}
		})
	}
}

func TestLoginProofUsesTOTPBudget(t *testing.T) {
	s, _, call := attemptHarness()
	caller := Caller{Peer: 1001, Kind: "user", Role: "read_only", UserID: "user", Session: "session"}
	req := Request{Verb: "session.login", Args: json.RawMessage(`{"totp":"invalid"}`)}
	if r := s.Dispatch(WithCaller(context.Background(), caller), req); r.Code != "unauthenticated" {
		t.Fatalf("first: %+v", r)
	}
	if r := call("session.reauth", "", ""); r.Code != "forbidden" {
		t.Fatalf("login bypassed TOTP: %+v", r)
	}
	// A refused combined attempt must not spend the remaining password attempt.
	if r := s.Dispatch(WithCaller(context.Background(), caller), req); r.Code != "forbidden" {
		t.Fatalf("second proof: %+v", r)
	}
	if r := call("session.login", "", ""); r.Code != "unauthenticated" {
		t.Fatalf("refusal spent password: %+v", r)
	}
}

func TestConcurrentAttemptsCannotExceedBudget(t *testing.T) {
	_, _, call := attemptHarness()
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if call("session.login", "", "").Code == "unauthenticated" {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := admitted.Load(); got != 2 {
		t.Fatalf("admitted %d, want 2", got)
	}
}

func TestCredentialBudgetDoesNotResetOnSuccessOrPayloadIdentity(t *testing.T) {
	s, _, call := attemptHarness()
	v := s.Registry["session.login"]
	v.Run = func(context.Context, *agent.Agent, any) (any, error) { return struct{}{}, nil }
	s.Registry["session.login"] = v
	for i := 0; i < 2; i++ {
		if r := call("session.login", "", ""); !r.OK {
			t.Fatalf("admission: %+v", r)
		}
	}
	if r := call("session.login", "different actor", "different session"); r.Code != "forbidden" {
		t.Fatalf("reset/bucket forgery: %+v", r)
	}
	if len(s.authRates) != 1 {
		t.Fatalf("payload allocated %d peer buckets", len(s.authRates))
	}
}

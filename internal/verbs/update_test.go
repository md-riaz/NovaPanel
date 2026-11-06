package verbs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdateDispatchClosedAndAuthorized(t *testing.T) {
	s, c, _ := sessionHarness(t)
	s.ConfigureJobs()
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: updateTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("network with unset trust or refused caller")
		return nil, nil
	})}
	defer func() { http.DefaultClient = old }()
	for _, tc := range []struct {
		verb       string
		caller     rpc.Caller
		args, code string
	}{
		{"update.check", rpc.Caller{Peer: 1001}, `{}`, "unauthenticated"},
		{"update.check", rpc.Caller{Kind: "user", Role: "read_only", Peer: 1001}, `{}`, "unsupported"},
		{"update.check", rpc.Caller{Kind: "token", Scopes: []string{"sites:read"}, Peer: 1001}, `{}`, "forbidden"},
		{"update.check", rpc.Caller{Kind: "token", Scopes: []string{"system:read"}, Peer: 1001}, `{}`, "unsupported"},
		{"update.check", rpc.RootCaller(0), `{"url":"https://evil.test"}`, "bad_request"},
		{"update.apply", rpc.Caller{Kind: "user", Role: "admin", Peer: 1001, MFAat: c.now}, `{}`, "forbidden"},
		{"update.apply", rpc.Caller{Kind: "user", Role: "owner", Peer: 1001}, `{}`, "forbidden"},
		{"update.apply", rpc.Caller{Kind: "token", Scopes: []string{"*"}, Automation: true, Peer: 1001}, `{}`, "forbidden"},
		{"update.apply", rpc.Caller{Kind: "user", Role: "owner", Peer: 1001, MFAat: c.now}, `{}`, "unsupported"},
	} {
		var events []rpc.AuditEvent
		s.DurableAudit = func(e rpc.AuditEvent) error { events = append(events, e); return nil }
		r := s.Dispatch(rpc.WithCaller(context.Background(), tc.caller), rpc.Request{ID: sessionID(t), Verb: tc.verb, Args: json.RawMessage(tc.args)})
		if r.OK || r.Code != tc.code {
			t.Fatalf("%s: %+v want %s", tc.verb, r, tc.code)
		}
		if len(events) == 0 || events[len(events)-1].Code != tc.code || events[len(events)-1].Verb != tc.verb {
			t.Fatalf("audit: %+v", events)
		}
	}
	rows, err := s.Agent.Store.ListJobs(context.Background(), store.JobFilter{})
	if err != nil || len(rows) != 0 {
		t.Fatalf("apply queued: %+v %v", rows, err)
	}
}

func TestUpdateRegistryContract(t *testing.T) {
	check, ok := rpc.Registry["update.check"]
	if !ok || check.MinRole != rpc.Admin || !check.ReadOnly || check.Scope != "system:read" || check.Slow || check.MFA || check.Effectful {
		t.Fatalf("check: %+v", check)
	}
	apply, ok := rpc.Registry["update.apply"]
	if !ok || apply.MinRole != rpc.Owner || apply.ReadOnly || apply.Scope != "" || !apply.Slow || !apply.MFA || !apply.Effectful || apply.Exec == nil || apply.ObjectKey(nil) != "system" {
		t.Fatalf("apply: %+v", apply)
	}
}

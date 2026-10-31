package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
)

func TestACMEWildcardRPCContract(t *testing.T) {
	f := newACMEWorkerFixture(t)
	ctx := rpc.WithCaller(context.Background(), rpc.RootCaller(0))
	for _, tc := range []struct{ verb, value, code string }{
		{"ssl.issue", "true", "unsupported"},
		{"ssl.issue", `"bogus"`, "bad_request"},
		{"ssl.issue", "false", "precondition_failed"},
		{"ssl.renew", "true", "bad_request"},
		{"ssl.renew", "false", "bad_request"},
	} {
		t.Run(tc.verb+tc.value, func(t *testing.T) {
			reply := f.server.Dispatch(ctx, rpc.Request{Verb: tc.verb, Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"wildcard":%s}`, f.sid, tc.value))})
			if reply.OK || reply.Code != tc.code {
				t.Fatalf("want %s, got %+v", tc.code, reply)
			}
		})
	}
	f.ca.mu.Lock()
	defer f.ca.mu.Unlock()
	if f.ca.requests != 0 {
		t.Fatalf("unsupported shape contacted CA: %d", f.ca.requests)
	}
}

func TestACMEWildcardWorkerRefusesDurableInput(t *testing.T) {
	f := newACMEWorkerFixture(t)
	ctx := rpc.WithCaller(context.Background(), rpc.RootCaller(0))
	queued := jobs.Job{ID: "wildcard-parent", Verb: "ssl.issue", ObjectKey: "site:" + f.sid, Role: "owner", CreatedBy: "local-root", Effectful: true, State: "running", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"staging":true,"wildcard":true}`, f.sid))}
	if err := f.a.Store.CreateJob(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
	err := f.server.ExecuteJob(ctx, &queued)
	coded, ok := err.(*rpc.CodedError)
	if !ok || coded.Code != "unsupported" {
		t.Fatalf("want unsupported, got %v", err)
	}
	f.ca.mu.Lock()
	defer f.ca.mu.Unlock()
	if f.ca.requests != 0 {
		t.Fatalf("worker contacted CA: %d", f.ca.requests)
	}
}

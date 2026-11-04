package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"testing"
)

func TestPHPListReadContract(t *testing.T) {
	// No store, queue or process runner is needed to read the supported policy.
	s := rpc.NewServer(agent.New())
	for _, caller := range []rpc.Caller{
		{Role: "read_only"}, {Role: "admin"}, {Role: "owner"},
		{Kind: "token", Session: "reader", Scopes: []string{"sites:read"}},
	} {
		r := s.Dispatch(rpc.WithCaller(context.Background(), caller), rpc.Request{Verb: "php.list", Args: json.RawMessage(`{}`)})
		if !r.OK {
			t.Fatalf("%+v: %+v", caller, r)
		}
		b, err := json.Marshal(r.Data)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"versions":["8.3"],"default":"8.3"}` {
			t.Fatal(string(b))
		}
	}
	v, ok := rpc.Registry["php.list"]
	if !ok || !v.ReadOnly || v.Slow || v.Effectful || v.Scope != "sites:read" || v.MinRole != rpc.ReadOnly {
		t.Fatalf("bad PHP read policy: %+v", v)
	}
	r := s.Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Kind: "token", Session: "wrong", Scopes: []string{"system:read"}}), rpc.Request{Verb: "php.list", Args: json.RawMessage(`{}`)})
	if r.Code != "forbidden" {
		t.Fatalf("missing scope accepted: %+v", r)
	}
	r = s.Dispatch(rpc.WithCaller(context.Background(), rpc.Caller{Role: "read_only"}), rpc.Request{Verb: "php.list", Args: json.RawMessage(`{"hardened":true}`)})
	if r.OK {
		t.Fatal("unsupported PHP policy accepted")
	}
}

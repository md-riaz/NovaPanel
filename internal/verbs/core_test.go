package verbs_test

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	_ "novapanel/internal/verbs"
	"testing"
)

func TestSeedVerbs(t *testing.T) {
	// The complete phase allowlist is enforced by TestRegisteredVerbs.
	s := rpc.NewServer(agent.New())
	for _, verb := range []string{"ping", "agent.info"} {
		r := s.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Verb: verb, Args: json.RawMessage(`{}`)})
		if !r.OK {
			t.Fatalf("%s: %+v", verb, r)
		}
		b, err := json.Marshal(r.Data)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		_ = json.Unmarshal(b, &data)
		if data["version"] == nil {
			t.Fatalf("missing version %s", b)
		}
		if verb == "agent.info" && (data["build"] == nil || data["started"] == nil || data["uptime_sec"] == nil) {
			t.Fatalf("missing info %s", b)
		}
	}
}

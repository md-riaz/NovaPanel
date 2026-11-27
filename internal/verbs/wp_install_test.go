package verbs

import (
	"context"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
)

func TestDeferredWordPressVerbs(t *testing.T) {
	for _, name := range []string{"wp.update", "wp.clone", "wp.staging"} {
		v, ok := rpc.Registry[name]
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		_, err := v.Run(context.Background(), &agent.Agent{}, v.NewArgs())
		coded, ok := err.(*rpc.CodedError)
		if !ok || coded.Code != "unsupported" {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

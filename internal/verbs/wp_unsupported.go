package verbs

import (
	"context"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
)

func init() {
	for _, name := range []string{"wp.update", "wp.clone", "wp.staging"} {
		rpc.Register(rpc.Verb{Name: name, MinRole: rpc.Admin, Scope: "sites:write", NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) {
			return nil, rpc.Errf("unsupported", "WordPress operation is unavailable")
		}})
	}
}

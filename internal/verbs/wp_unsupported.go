package verbs

import (
	"context"

	"novapanel/internal/agent"
	"novapanel/internal/rpc"
)

func init() {
	rpc.Register(rpc.Verb{Name: "wp.update", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: wpUnsupportedArgs, Run: wpUnsupported})
	rpc.Register(rpc.Verb{Name: "wp.clone", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: wpUnsupportedArgs, Run: wpUnsupported})
	rpc.Register(rpc.Verb{Name: "wp.staging", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: wpUnsupportedArgs, Run: wpUnsupported})
}

func wpUnsupportedArgs() any { return &map[string]any{} }
func wpUnsupported(context.Context, *agent.Agent, any) (any, error) {
	return nil, rpc.Errf("unsupported", "WordPress operation is unavailable")
}

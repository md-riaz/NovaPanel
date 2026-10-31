package verbs

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
)

func init() {
	rpc.Register(rpc.Verb{Name: "php.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &emptyArgs{} }, Run: phpList})
}

// phpList reports the supported version policy, not host package discovery.
// Installation and service readiness remain mutation preconditions.
func phpList(context.Context, *agent.Agent, any) (any, error) {
	return struct {
		Versions []string `json:"versions"`
		Default  string   `json:"default"`
	}{[]string{"8.3"}, "8.3"}, nil
}

package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func init() {
	rpc.Register(rpc.Verb{Name: "php.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &emptyArgs{} }, Run: phpList})
	rpc.Register(rpc.Verb{Name: "php.set", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &phpSetArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*phpSetArgs).request.SiteID }, Run: phpSet, Exec: phpSetExec})
}

type phpSetArgs struct{ request php.SetRequest }

func (v *phpSetArgs) UnmarshalJSON(raw []byte) error {
	request, err := php.DecodeSetRequest(raw)
	if err != nil {
		return err
	}
	v.request = request
	return nil
}

func phpSet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	request := args.(*phpSetArgs).request
	return a.Jobs.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: request.SiteID, PHP: &request}, nil)
}

// Execution must fail closed until FPM publication and recovery are wired.
func phpSetExec(context.Context, *agent.Agent, *jobs.Job) error {
	return rpc.Errf("unsupported", "PHP execution unavailable")
}

var _ json.Unmarshaler = (*phpSetArgs)(nil)

// phpList reports the supported version policy, not host package discovery.
// Installation and service readiness remain mutation preconditions.
func phpList(context.Context, *agent.Agent, any) (any, error) {
	return struct {
		Versions []string `json:"versions"`
		Default  string   `json:"default"`
	}{[]string{"8.3"}, "8.3"}, nil
}

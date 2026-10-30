package verbs

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/platform"
	"novapanel/internal/rpc"
	"novapanel/internal/validate"
	"strings"
)

type unitArgs struct {
	Unit string `json:"unit"`
}

func (a *unitArgs) Validate() error {
	if err := (&validate.V{}).UnitName(a.Unit); err != nil {
		return err
	}
	if !platform.ManageableUnit(a.Unit) {
		return rpc.Errf("invalid_argument", "Unit is not manageable")
	}
	return nil
}

type serviceStatusReply struct {
	Unit    string `json:"unit"`
	Active  bool   `json:"active"`
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
}

func init() {
	rpc.Register(rpc.Verb{Name: "service.status", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &unitArgs{} }, Run: serviceStatus})
}
func serviceStatus(ctx context.Context, a *agent.Agent, args any) (any, error) {
	unit := args.(*unitArgs).Unit
	out, _, code, err := a.Exec.Run(ctx, platform.Systemctl, []string{"is-active", unit}, exec.RunOpts{})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && code != 3 && code != 4 {
		return nil, err
	}
	if code != 0 && code != 3 && code != 4 {
		return nil, rpc.Errf("agent_error", "Cannot query service state")
	}
	state := strings.TrimSpace(string(out))
	if state == "" {
		return nil, rpc.Errf("agent_error", "Empty service state")
	}
	enabled, _, code, err := a.Exec.Run(ctx, platform.Systemctl, []string{"is-enabled", unit}, exec.RunOpts{})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && code != 1 && code != 3 && code != 4 {
		return nil, err
	}
	if code != 0 && code != 1 && code != 3 && code != 4 {
		return nil, rpc.Errf("agent_error", "Cannot query service enablement")
	}
	return serviceStatusReply{unit, state == "active", strings.TrimSpace(string(enabled)) == "enabled", state}, nil
}

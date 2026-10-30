package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/jobs"
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
	rpc.Register(rpc.Verb{Name: "service.restart", MinRole: rpc.Admin, Scope: "system:write", Slow: true, Effectful: true, NewArgs: func() any { return &unitArgs{} }, ObjectKey: func(args any) string { return "service:" + args.(*unitArgs).Unit }, Run: serviceRestart, Exec: serviceRestartExec})
	rpc.Register(rpc.Verb{Name: "service.status", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &unitArgs{} }, Run: serviceStatus})
}
func serviceRestart(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return a.Jobs.Enqueue(ctx, "service.restart", args, "service:"+args.(*unitArgs).Unit)
}
func serviceRestartExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var args unitArgs
	if e := json.Unmarshal(j.Args, &args); e != nil {
		return rpc.Errf("bad_request", "Invalid restart arguments")
	}
	if e := args.Validate(); e != nil {
		return e
	}
	_, _, code, e := a.Exec.Run(ctx, platform.Systemctl, []string{"restart", args.Unit}, exec.RunOpts{})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil {
		return e
	}
	if code != 0 {
		return rpc.Errf("agent_error", "Cannot restart service")
	}
	j.Result, e = json.Marshal(struct {
		Unit        string `json:"unit"`
		RestartedAt int64  `json:"restarted_at"`
	}{args.Unit, a.Now()})
	return e
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

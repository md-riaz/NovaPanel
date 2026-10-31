package verbs

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/firewall"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
)

type firewallApplyArgs struct {
	Rules     []firewall.Rule `json:"rules"`
	WindowMin int             `json:"window_min,omitempty"`
}

func (args *firewallApplyArgs) Validate() error {
	if args.WindowMin < 0 {
		return rpc.Errf("invalid_argument", "Invalid firewall window")
	}
	// DefaultIncoming is deliberately not an RPC field. The canonical 03 shape
	// omits it; the renderer retains its trusted default.
	_, err := firewall.Render(firewall.Desired{Rules: args.Rules}, 22, "")
	return err
}

type firewallPendingArgs struct {
	PendingID string `json:"pending_id"`
}

func init() {
	rpc.Register(rpc.Verb{Name: "firewall.apply", MinRole: rpc.Admin, Scope: "firewall:write", Slow: true, Effectful: true, MFA: true, NewArgs: func() any { return &firewallApplyArgs{} }, ObjectKey: func(any) string { return "firewall" }, Run: firewallApply, Exec: firewallApplyExec})
	rpc.Register(rpc.Verb{Name: "firewall.confirm", MinRole: rpc.Admin, Scope: "firewall:write", MFA: true, NewArgs: func() any { return &firewallPendingArgs{} }, ObjectKey: func(any) string { return "firewall" }, Run: firewallConfirm})
	rpc.Register(rpc.Verb{Name: "firewall.revert", MinRole: rpc.Admin, Scope: "firewall:write", MFA: true, NewArgs: func() any { return &firewallPendingArgs{} }, ObjectKey: func(any) string { return "firewall" }, Run: firewallRevert})
}
func firewallApply(context.Context, *agent.Agent, any) (any, error) {
	return nil, rpc.Errf("unsupported", "Durable firewall safe-apply is not configured")
}
func firewallApplyExec(context.Context, *agent.Agent, *jobs.Job) error {
	return rpc.Errf("unsupported", "Durable firewall safe-apply is not configured")
}
func firewallConfirm(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if a.FirewallRecovery == nil {
		return nil, rpc.Errf("unsupported", "Firewall recovery is not configured")
	}
	if err := a.FirewallRecovery.Confirm(ctx, args.(*firewallPendingArgs).PendingID); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}
func firewallRevert(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if a.FirewallRecovery == nil {
		return nil, rpc.Errf("unsupported", "Firewall recovery is not configured")
	}
	if err := a.FirewallRecovery.Revert(ctx, args.(*firewallPendingArgs).PendingID); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

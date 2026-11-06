package verbs

import (
	"context"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/update"
	"novapanel/internal/version"
)

func init() {
	rpc.Register(rpc.Verb{Name: "update.check", MinRole: rpc.Admin, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &emptyArgs{} }, Run: updateCheck})
	rpc.Register(rpc.Verb{Name: "update.apply", MinRole: rpc.Owner, Slow: true, Effectful: true, MFA: true, NewArgs: func() any { return &emptyArgs{} }, ObjectKey: func(any) string { return "system" }, Run: updateApply, Exec: updateApplyExec})
}

func updateCheck(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	if err := update.Enabled(); err != nil {
		return nil, err
	}
	ms := a.Cfg.DeadlineDefaultMs
	if ms <= 0 {
		ms = 120000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()
	m, _, err := update.Check(ctx, a.Cfg.UpdateManifestURL, version.Version)
	if err != nil {
		return nil, err
	}
	return struct {
		Current     string `json:"current"`
		Latest      string `json:"latest,omitempty"`
		SecurityFix bool   `json:"security_fix,omitempty"`
		Notes       string `json:"notes,omitempty"`
	}{version.Version, m.Version, m.SecurityFix, m.Notes}, nil
}

func updateApply(context.Context, *agent.Agent, any) (any, error) {
	if err := update.Enabled(); err != nil {
		return nil, err
	}
	// shortcut: no jobs until independent helper lifetime and durable reconciliation are proven.
	return nil, rpc.Errf("unsupported", "Independent update helper unavailable")
}
func updateApplyExec(context.Context, *agent.Agent, *jobs.Job) error {
	return rpc.Errf("unsupported", "Independent update helper unavailable")
}

// Package agent holds dependencies without importing the RPC transport.
package agent

import (
	"context"
	"novapanel/internal/audit"
	"novapanel/internal/auth"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/jobs"
	"novapanel/internal/platform"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"time"
)

type Clock interface{ Now() int64 }
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type Agent struct {
	firewallStartup         func(context.Context) error
	firewallTerminalJournal func(context.Context) (*firewall.Journal, error)
	// Nil until durable snapshot recovery and independent deadman ownership are wired.
	FirewallRecovery *firewall.Recovery
	ProvisionSite    func(context.Context, string) error
	Nginx            *nginx.Applier
	PasswordFailures auth.FailureBudgets
	UnknownFailures  auth.FailureBudgets
	MFAFailures      auth.FailureBudgets
	Jobs             *jobs.Queue
	Scheduler        *jobs.Scheduler
	Store            *store.Store
	Secret           *secret.Sealer
	Audit            *audit.Log
	Cfg              config.Config
	Exec             exec.Runner
	Validate         *validate.V
	Clock            Clock
	Started          int64
}

func New() *Agent { return NewWithDependencies(config.Defaults(), exec.Real{}, &validate.V{}) }
func NewWithDependencies(cfg config.Config, runner exec.Runner, v *validate.V) *Agent {
	c := realClock{}
	a := &Agent{Cfg: cfg, Exec: runner, Validate: v, Clock: c, Started: c.Now(),
		firewallStartup: func(ctx context.Context) error { return firewall.PreflightInstalledFirewall(ctx, runner) },
		ProvisionSite: func(ctx context.Context, name string) error {
			return platform.EnsureSite(ctx, runner, cfg.SitesRoot, cfg.ACMEWebroot, name)
		},
		Nginx: &nginx.Applier{Root: platform.NginxRoot, Runner: runner, Probe: nginx.StateProbe("http://127.0.0.1:80", "https://127.0.0.1:443", nil)},
	}
	a.Nginx.Guard = func(ctx context.Context) error {
		if a.Store == nil {
			return nil
		}
		b, err := a.Store.TLSIntent(ctx)
		if err != nil {
			return err
		}
		if len(b) > 0 {
			return rpcerr.Errf("precondition_failed", "Unresolved TLS publication")
		}
		return nil
	}
	return a
}
func (a *Agent) Now() int64 {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now().UnixMilli()
}

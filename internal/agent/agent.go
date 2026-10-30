// Package agent holds dependencies without importing the RPC transport.
package agent

import (
	"context"
	"novapanel/internal/audit"
	"novapanel/internal/auth"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/platform"
	"novapanel/internal/render/nginx"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"time"
)

type Clock interface{ Now() int64 }
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type Agent struct {
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
	return &Agent{Cfg: cfg, Exec: runner, Validate: v, Clock: c, Started: c.Now(),
		ProvisionSite: func(ctx context.Context, name string) error {
			return platform.EnsureSite(ctx, runner, cfg.SitesRoot, cfg.ACMEWebroot, name)
		},
		Nginx: &nginx.Applier{Root: platform.NginxRoot, Runner: runner, Probe: nginx.HTTPProbe("http://127.0.0.1:80")},
	}
}
func (a *Agent) Now() int64 {
	if a.Clock != nil {
		return a.Clock.Now()
	}
	return time.Now().UnixMilli()
}

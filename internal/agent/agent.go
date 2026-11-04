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
	"novapanel/internal/render/php"
	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"sync/atomic"
	"time"
)

// PHPDependencies is trusted configuration, not request input. The resolver must
// verify the provisioned site's identity. RecoveryRoot requires an owner capable
// of reconciling snapshots across startup; no default is installed.
type PHPDependencies struct {
	Pools           *php.Applier
	RecoveryRoot    string
	ResolveIdentity func(context.Context, store.Site) (uint32, uint32, error)
}

type Clock interface{ Now() int64 }
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type Agent struct {
	mutationsBlocked atomic.Bool
	ProvisionSite    func(context.Context, string) error
	Nginx            *nginx.Applier
	// PHP publication is opt-in: no production recovery owner exists yet.
	PHP              *PHPDependencies
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

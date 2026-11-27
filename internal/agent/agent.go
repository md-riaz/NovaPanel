// Package agent holds dependencies without importing the RPC transport.
package agent

import (
	"context"
	"novapanel/internal/audit"
	"novapanel/internal/auth"
	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/platform"
	"novapanel/internal/render/nginx"
	renderphp "novapanel/internal/render/php"
	"novapanel/internal/render/sshd"
	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"sync"
	"sync/atomic"
	"time"
)

// PHPDependencies is trusted configuration, not request input. The resolver must
// verify the provisioned site's identity. RecoveryRoot requires an owner capable
// of reconciling snapshots across startup; no default is installed.
type PHPDependencies struct {
	Pools           *renderphp.Applier
	RecoveryRoot    string
	ResolveIdentity func(context.Context, store.Site) (uint32, uint32, error)
}

type Clock interface{ Now() int64 }
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type Agent struct {
	UploadsMu            sync.Mutex
	mutationsBlocked     atomic.Bool
	DurablePHP           *PHPDependencies
	ProvisionSite        func(context.Context, string, string) error
	DeleteDatabaseForJob func(context.Context, string, string, string) error
	SSHD                 *sshd.Applier
	Nginx                *nginx.Applier
	PHP                  *php.PoolApplier
	PasswordFailures     auth.FailureBudgets
	UnknownFailures      auth.FailureBudgets
	MFAFailures          auth.FailureBudgets
	Jobs                 *jobs.Queue
	Scheduler            *jobs.Scheduler
	Store                *store.Store
	Secret               *secret.Sealer
	Audit                *audit.Log
	Cfg                  config.Config
	Exec                 exec.Runner
	Validate             *validate.V
	Clock                Clock
	Started              int64
}

func New() *Agent { return NewWithDependencies(config.Defaults(), exec.Real{}, &validate.V{}) }

type maintenanceFinalizerKey struct{}

func MaintenanceFinalizer(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, maintenanceFinalizerKey{}, jobID)
}

func (a *Agent) TLSBlocksMutation(ctx context.Context) (bool, error) {
	if jobID, ok := ctx.Value(maintenanceFinalizerKey{}).(string); ok {
		return a.Store.FinalizerTLSBlocked(ctx, jobID)
	}
	b, err := a.Store.TLSIntent(ctx)
	return len(b) > 0, err
}

func NewWithDependencies(cfg config.Config, runner exec.Runner, v *validate.V) *Agent {
	c := realClock{}
	a := &Agent{Cfg: cfg, Exec: runner, Validate: v, Clock: c, Started: c.Now(),
		ProvisionSite: func(ctx context.Context, name, siteID string) error {
			return platform.EnsureSite(ctx, runner, cfg.SitesRoot, cfg.ACMEWebroot, cfg.LogDir, name, siteID)
		},
		SSHD:  &sshd.Applier{Runner: runner},
		Nginx: &nginx.Applier{Root: platform.NginxRoot, Runner: runner, Probe: nginx.StateProbe("http://127.0.0.1:80", "https://127.0.0.1:443", nil)},
		PHP: &php.PoolApplier{Dir: "/etc/php/8.3/fpm/pool.d", Runner: runner, Probe: func(ctx context.Context) error {
			_, _, code, err := runner.Run(ctx, platform.Systemctl, []string{"is-active", "--quiet", "php8.3-fpm.service"}, exec.RunOpts{})
			if err != nil {
				return err
			}
			if code != 0 {
				return rpcerr.Errf("agent_error", "FPM health probe failed")
			}
			return nil
		}},
	}
	a.Nginx.Guard = func(ctx context.Context) error {
		if a.Store == nil {
			return nil
		}
		blocked, err := a.TLSBlocksMutation(ctx)
		if err != nil {
			return err
		}
		if blocked {
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

package verbs

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/database"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/platform"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

func freshWordPressSiteCreateExec(ctx context.Context, a *agent.Agent, j *jobs.Job, siteID string) error {
	if a.Jobs == nil || a.Store == nil || a.Exec == nil || a.PHP == nil || a.Nginx == nil || !id.Valid(siteID) || j.ObjectKey != "site:"+siteID {
		return rpc.Errf("precondition_failed", "WordPress site provisioning unavailable")
	}
	op, err := a.Store.LoadOperation(ctx, j.ID)
	if err != nil {
		return err
	}
	if op.Kind != store.WordPressInstall || op.SiteID != siteID || op.Identity.WordPress == nil {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	identity, err := a.Store.LoadSiteLifetime(ctx, siteID)
	if err != nil {
		return err
	}
	in := op.Identity.WordPress.Input
	if identity.AllocatingJob != j.ID || identity.Name != in.SiteName || in.SitesRoot != a.Cfg.SitesRoot || identity.Docroot != filepath.Join(in.SitesRoot, in.SiteName, "public") || identity.UID != in.UID || identity.GID != in.GID || identity.UID != identity.GID || identity.UID < 2000000 || identity.UID > 2999999 {
		return rpc.Errf("precondition_failed", "Reserved site identity changed")
	}
	if j.Step == store.FreshWordPressRollbackStep || j.Step == store.FreshWordPressRollbackCleanedStep {
		return rollbackFreshWordPress(ctx, a, j, op, identity)
	}
	if err = func() error {
		unlock, err := a.Jobs.LockSiteOSUser(ctx, siteID, identity.Name)
		if err != nil {
			return err
		}
		defer unlock()
		if err = a.Store.CheckOperation(ctx, j.ID); err != nil {
			return err
		}
		if err = a.Store.CheckFreshSiteLifetime(ctx, identity); err != nil {
			if !errors.Is(err, store.ErrSitePrecondition) {
				return err
			}
			return a.Store.CheckActivatedSiteLifetime(ctx, identity, j.ID)
		}
		if err = platform.EnsureFreshSite(ctx, a.Exec, a.Cfg.SitesRoot, a.Cfg.LogDir, identity.Name, identity.SiteID, identity.AllocatingJob, identity.UID); err != nil {
			return err
		}
		if op.Checkpoint != "prepared" {
			return nil
		}
		credentials, err := a.Jobs.OpenWordPressCredentials(ctx, j.ID)
		if err != nil {
			return err
		}
		if err = databaseReady(ctx, a.Exec); err != nil {
			return err
		}
		account := op.Identity.Accounts[0]
		if err = database.ManagedCreate(ctx, a.Exec, op.DatabaseID, account.ID, op.Identity.Database.Name, account.Username, credentials.DatabasePassword); err != nil {
			return jobs.ErrRetry
		}
		if err = a.Store.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
			return jobs.ErrRetry
		}
		return nil
	}(); err != nil {
		return err
	}
	return a.Jobs.InstallFreshWordPress(ctx, j.ID, identity, func() error {
		limits, err := a.Store.SitePHPLimits(ctx, siteID)
		if err != nil {
			return err
		}
		cap, err := committedPHPCap(limits)
		if err != nil {
			return err
		}
		if err = a.PHP.Recover(ctx, identity.Name, php.PoolState{SiteID: siteID, Exists: true, MaxChildren: cap, Limits: limits}); err != nil {
			return err
		}
		version := "8.3"
		return a.Nginx.Apply(ctx, nginx.Desired{Name: identity.Name, Domains: []string{op.Identity.WordPress.Input.PrimaryDomain}, Docroot: identity.Docroot, PHPVersion: &version, ACMERoot: filepath.Join(a.Cfg.ACMEWebroot, identity.Name)})
	})
}

// rollbackFreshWordPress keeps site and account locks while all external stores
// are torn down, then durably marks cleanup before removing custody material.
func rollbackFreshWordPress(ctx context.Context, a *agent.Agent, j *jobs.Job, op store.Operation, identity store.SiteLifetime) error {
	unlock, err := a.Jobs.LockSiteOSUser(ctx, identity.SiteID, identity.Name)
	if err != nil {
		return errors.Join(jobs.ErrRetry, err)
	}
	defer unlock()
	if err = a.Store.CheckFreshSiteLifetime(ctx, identity); err != nil {
		return errors.Join(jobs.ErrRetry, err)
	}
	if j.Step == store.FreshWordPressRollbackStep {
		domain, err := platform.CreateInstallDomain(identity.SiteID)
		if err != nil {
			return errors.Join(jobs.ErrRetry, err)
		}
		err = domain.WithEmpty(ctx, func(work context.Context) error {
			if err := platform.VerifyFreshSitePaths(a.Cfg.SitesRoot, a.Cfg.LogDir, identity.Name, identity.SiteID, identity.AllocatingJob, identity.UID); err != nil {
				return err
			}
			if err := php.DeleteReservedUser(work, a.Exec, a.Cfg.SitesRoot, identity.Name, identity.SiteID, identity.UID); err != nil {
				return err
			}
			if err := platform.RemoveFreshSitePaths(a.Cfg.SitesRoot, a.Cfg.LogDir, identity.Name, identity.SiteID, identity.AllocatingJob, identity.UID); err != nil {
				return err
			}
			if err := databaseReady(work, a.Exec); err != nil {
				return err
			}
			account := op.Identity.Accounts[0]
			return database.RollbackFreshCreate(work, a.Exec, op.Identity.Database.Name, op.DatabaseID, account.Username, account.ID)
		})
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		closeErr := domain.Close(closeCtx)
		cancel()
		if err = errors.Join(err, closeErr); err != nil {
			return errors.Join(jobs.ErrRetry, err)
		}
		if err = a.Store.MarkFreshWordPressRollbackCleaned(ctx, j.ID); err != nil {
			return errors.Join(jobs.ErrRetry, err)
		}
		j.Step = store.FreshWordPressRollbackCleanedStep
	}
	if err = a.Jobs.CleanupFreshWordPressRollbackConfig(ctx, j.ID); err != nil {
		return errors.Join(jobs.ErrRetry, err)
	}
	code := j.Code
	if code == "" {
		code = "agent_error"
	}
	return rpc.Errf(code, "WordPress installation failed")
}

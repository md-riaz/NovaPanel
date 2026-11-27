package verbs

import (
	"context"
	"errors"
	"path/filepath"

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
	if identity.AllocatingJob != j.ID || identity.UID != identity.GID || identity.UID < 2000000 || identity.UID > 2999999 {
		return rpc.Errf("precondition_failed", "Reserved site identity changed")
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
		if err = platform.EnsureFreshSite(ctx, a.Exec, a.Cfg.SitesRoot, a.Cfg.LogDir, identity.Name, identity.SiteID, identity.UID); err != nil {
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

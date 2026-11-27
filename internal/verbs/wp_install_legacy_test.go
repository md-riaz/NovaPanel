package verbs

import (
	"context"
	"database/sql"
	"errors"

	"novapanel/internal/agent"
	"novapanel/internal/database"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

type wpInstallArgs struct {
	SiteID        string `json:"site_id"`
	Title         string `json:"title"`
	AdminUsername string `json:"admin_username"`
	AdminEmail    string `json:"admin_email"`
}

type wpInstallJobArgs struct {
	SiteID string `json:"site_id"`
}

func (v *wpInstallJobArgs) Validate() error {
	if !id.Valid(v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	return nil
}

func (v *wpInstallArgs) Validate() error {
	if !id.Valid(v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	return wordpress.ValidateAdministrator(wordpress.Administrator{Title: v.Title, Username: v.AdminUsername, Email: v.AdminEmail})
}

func wpInstall(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if !wpLifetimeAvailable() {
		return nil, rpc.Errf("unsupported", "WordPress installation requires protected lifetime isolation")
	}
	v := args.(*wpInstallArgs)
	site, err := a.Store.SiteByID(ctx, v.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, rpc.Errf("precondition_failed", "WordPress site is not pending installation")
	}
	if err != nil {
		return nil, err
	}
	if site.Type != "wordpress" || site.Status != "active" {
		return nil, rpc.Errf("precondition_failed", "WordPress site is not pending installation")
	}
	_, err = a.Store.WordPressDatabaseID(ctx, site.ID)
	if err == nil {
		return nil, rpc.Errf("precondition_failed", "WordPress site is already installed")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if a.Exec == nil {
		return nil, rpc.Errf("precondition_failed", "Site account is unavailable")
	}
	uid, gid, err := php.ResolveUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID)
	if err != nil {
		return nil, rpc.Errf("precondition_failed", "Site account is unavailable")
	}
	domains, err := a.Store.ListDomains(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	var domain store.Domain
	for _, d := range domains {
		if d.IsPrimary {
			domain = d
			break
		}
	}
	if domain.ID == "" {
		return nil, rpc.Errf("precondition_failed", "Primary domain unavailable")
	}
	evidence, ok := wpEvidence()
	if !ok {
		return nil, rpc.Errf("unsupported", "WordPress artifact staging is unavailable")
	}
	credentials, err := jobs.GenerateWordPressCredentials()
	if err != nil {
		return nil, err
	}
	return a.Jobs.EnqueueWordPress(ctx, store.WordPressReservation{SiteID: site.ID, DomainID: domain.ID, Input: wordpress.InstallInput{SitesRoot: a.Cfg.SitesRoot, SiteName: site.Name, PrimaryDomain: domain.FQDN, UID: uid, GID: gid, Locale: wordpress.ApprovedArtifacts().Locale, Administrator: wordpress.Administrator{Title: v.Title, Username: v.AdminUsername, Email: v.AdminEmail}}, Evidence: evidence}, credentials)
}

func wpInstallExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	if !wpLifetimeAvailable() {
		return rpc.Errf("unsupported", "WordPress installation requires protected lifetime isolation")
	}
	op, err := a.Store.LoadOperation(ctx, j.ID)
	if err != nil {
		return jobs.ErrRetry
	}
	if op.Kind != store.WordPressInstall || j.ObjectKey != "site:"+op.SiteID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	if op.Checkpoint == "done" {
		return nil
	}
	if err = a.Store.CheckOperation(ctx, j.ID); err != nil {
		return err
	}
	credentials, err := a.Jobs.OpenWordPressCredentials(ctx, j.ID)
	if err != nil {
		return jobs.ErrRetry
	}
	if op.Checkpoint == "prepared" {
		if err = php.VerifyUser(ctx, a.Exec, a.Cfg.SitesRoot, op.Identity.WordPress.Input.SiteName, op.SiteID); err != nil {
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
	}
	if op.Checkpoint == "prepared" {
		op.Checkpoint = "db_applied"
	}
	return wpInstallTail(ctx, a.Store, a.Exec, a.Cfg.SitesRoot, j, op, credentials)
}

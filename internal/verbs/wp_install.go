package verbs

import (
	"context"
	"database/sql"
	"errors"

	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/rpc"
	"novapanel/internal/wordpress"
)

type wpInstallArgs struct {
	SiteID        string `json:"site_id"`
	Title         string `json:"title"`
	AdminUsername string `json:"admin_username"`
	AdminEmail    string `json:"admin_email"`
}

func (v *wpInstallArgs) Validate() error {
	if !id.Valid(v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	return wordpress.ValidateAdministrator(wordpress.Administrator{Title: v.Title, Username: v.AdminUsername, Email: v.AdminEmail})
}

func init() {
	rpc.Register(rpc.Verb{Name: "wp.install", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &wpInstallArgs{} }, ObjectKey: func(args any) string { return "site:" + args.(*wpInstallArgs).SiteID }, Run: wpInstall, Exec: wpInstallExec})
}

func wpInstall(ctx context.Context, a *agent.Agent, args any) (any, error) {
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
	if err = php.VerifyUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID); err != nil {
		return nil, rpc.Errf("precondition_failed", "Site account is unavailable")
	}
	return nil, rpc.Errf("unsupported", "WordPress installation is unavailable")
}

func wpInstallExec(context.Context, *agent.Agent, *jobs.Job) error {
	return rpc.Errf("unsupported", "WordPress installation is unavailable")
}

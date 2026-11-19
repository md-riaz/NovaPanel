//go:build wordpress_offline_lab

package verbs

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
	"novapanel/internal/wordpress/offline"
)

func wpInstallTail(ctx context.Context, st *store.Store, exec runner.Runner, root string, j *jobs.Job, op store.Operation, credentials jobs.WordPressCredentials) error {
	in := op.Identity.WordPress.Input
	if op.Checkpoint == "db_applied" {
		docroot := filepath.Join(root, in.SiteName, "public")
		entries, err := os.ReadDir(docroot)
		if err != nil {
			return rpc.Errf("agent_error", "WordPress docroot unavailable")
		}
		if len(entries) != 0 {
			return rpc.Errf("precondition_failed", "WordPress docroot requires operator reconciliation")
		}
		uid, gid, err := php.ResolveUser(ctx, exec, root, in.SiteName, op.SiteID)
		if err != nil || uid != in.UID || gid != in.GID {
			return rpc.Errf("agent_error", "Site account is unavailable")
		}
		cfg, err := configcustody.ComposeConfig("d_"+strings.ToLower(in.DatabaseID), "u_"+strings.ToLower(in.DatabaseUserID), credentials.DatabasePassword, credentials.Salts)
		if err != nil {
			return rpc.Errf("agent_error", "WordPress configuration failed")
		}
		defer clear(cfg)
		lt := offline.NewLabLifetime(uid, gid, in.SiteName, op.SiteID, filepath.Join(root, in.SiteName, "public"))
		if err = offline.InstallWordPress(lt, cfg); err != nil {
			return rpc.Errf("agent_error", "WordPress artifact installation failed")
		}
		if err = st.CheckpointOperation(ctx, j.ID, "db_applied", "config_published"); err != nil {
			return jobs.ErrRetry
		}
		op.Checkpoint = "config_published"
	}
	if op.Checkpoint == "config_published" {
		plan, err := wordpress.BuildInstall(in, op.Identity.WordPress.Evidence)
		if err != nil {
			return rpc.Errf("agent_error", "WordPress install plan failed")
		}
		installed := false
		for _, step := range plan.Steps {
			if step.Kind == wordpress.IsInstalled {
				_, _, code, err := exec.Run(ctx, step.Program, step.Args, runner.RunOpts{Dir: plan.Dir, Env: plan.Env, Uid: plan.UID, Gid: plan.GID, Groups: plan.Groups})
				if err != nil {
					return rpc.Errf("agent_error", "WordPress installation check failed")
				}
				installed = code == 0
				continue
			}
			if step.Kind != wordpress.InstallCore || installed {
				continue
			}
			_, _, code, err := exec.Run(ctx, step.Program, step.Args, runner.RunOpts{Dir: plan.Dir, Env: plan.Env, Uid: plan.UID, Gid: plan.GID, Groups: plan.Groups, Stdin: strings.NewReader(credentials.AdminPassword + "\n")})
			if err != nil || code != 0 {
				return rpc.Errf("agent_error", "WordPress core installation failed")
			}
		}
		if err = st.CheckpointOperation(ctx, j.ID, "config_published", "verified"); err != nil {
			return jobs.ErrRetry
		}
	}
	return nil
}

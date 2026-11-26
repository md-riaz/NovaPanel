package verbs

import (
	"context"
	"encoding/json"

	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/render/sshd"
	"novapanel/internal/rpc"
)

type sftpOnlyArgs struct {
	SiteID  string `json:"site_id"`
	Enabled *bool  `json:"enabled"`
}

func (v *sftpOnlyArgs) Validate() error {
	if !id.Valid(v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	if v.Enabled == nil {
		return rpc.Errf("bad_request", "enabled is required")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "sshkey.sftp_only", MinRole: rpc.Admin, Scope: "ssh:write", Slow: true, MFA: true, NewArgs: func() any { return &sftpOnlyArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*sftpOnlyArgs).SiteID }, Run: sftpOnly, Exec: sftpOnlyExec})
}
func sftpOnly(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*sftpOnlyArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if a.Jobs == nil {
		return nil, rpc.Errf("agent_error", "Job queue unavailable")
	}
	return a.Jobs.Enqueue(ctx, "sshkey.sftp_only", v, "site:"+v.SiteID)
}
func sftpOnlyExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v sftpOnlyArgs
	if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil || j.ObjectKey != "site:"+v.SiteID {
		return rpc.Errf("invalid_argument", "Invalid SFTP job")
	}
	site, err := a.Store.SiteByID(ctx, v.SiteID)
	if err != nil {
		return siteError(err)
	}
	if site.Status != "active" {
		return rpc.Errf("precondition_failed", "Site unavailable")
	}
	if _, _, err = php.ResolveUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID); err != nil {
		return err
	}
	if err = applySSHD(ctx, a, sshd.Desired{Site: site.Name, User: site.Name, Enabled: *v.Enabled}); err != nil {
		return err
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

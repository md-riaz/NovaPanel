package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/platform"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"os"
	"path/filepath"
)

type siteDeleteArgs struct {
	SiteID     string `json:"site_id"`
	PurgeFiles bool   `json:"purge_files,omitempty"`
}

func (v *siteDeleteArgs) Validate() error { return (&siteIDArgs{v.SiteID}).Validate() }
func init() {
	rpc.Register(rpc.Verb{Name: "site.delete", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, MFA: true, NewArgs: func() any { return &siteDeleteArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*siteDeleteArgs).SiteID }, Run: siteDelete, Exec: siteDeleteExec})
}
func siteDelete(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*siteDeleteArgs)
	if _, e := a.Store.SiteByID(ctx, v.SiteID); e != nil {
		return nil, siteError(e)
	}
	return a.Jobs.Enqueue(ctx, "site.delete", v, "site:"+v.SiteID)
}
func siteDeleteExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v siteDeleteArgs
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return e
	}
	if e := v.Validate(); e != nil {
		return e
	}
	s, e := a.Store.SiteByID(ctx, v.SiteID)
	if errors.Is(e, sql.ErrNoRows) {
		j.Result = json.RawMessage(`{}`)
		return nil
	}
	if e != nil {
		return e
	}
	if e = (&validate.V{}).SiteName(s.Name); e != nil {
		return e
	}
	if s.Docroot != filepath.Join(a.Cfg.SitesRoot, s.Name, "public") {
		return rpc.Errf("precondition_failed", "Unexpected site docroot")
	}
	if s.Status != "deleting" {
		if e = a.Store.SetSiteStatus(ctx, s.ID, "deleting"); e != nil {
			return e
		}
	}
	if a.Nginx == nil {
		return rpc.Errf("agent_error", "Nginx unavailable")
	}
	if e = a.Nginx.Remove(ctx, s.Name); e != nil {
		return e
	}
	ds, e := a.Store.ListDomains(ctx, s.ID)
	if e != nil {
		return e
	}
	if e = removeSiteCertificates(a.Cfg.TLSDir, ds); e != nil {
		return e
	}
	if e = a.Store.ClearSiteCertificates(ctx, s.ID); e != nil {
		return e
	}
	if v.PurgeFiles {
		if e = platform.RemoveSite(ctx, a.Exec, a.Cfg.SitesRoot, s.Name); e != nil {
			return e
		}
	}
	if e = a.Store.DeleteSite(ctx, s.ID); e != nil {
		return siteError(e)
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}
func removeSiteCertificates(path string, domains []store.Domain) error {
	resolved, e := filepath.EvalSymlinks(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !filepath.IsAbs(path) || resolved != path {
		return rpc.Errf("invalid_argument", "Invalid TLS directory")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return e
	}
	defer root.Close()
	for _, d := range domains {
		if e = (&validate.V{}).Hostname(d.FQDN); e != nil {
			return e
		}
	}
	for _, d := range domains {
		if e = root.RemoveAll(d.FQDN); e != nil {
			return e
		}
	}
	return nil
}

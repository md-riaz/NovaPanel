package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/platform"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"os"
	"path/filepath"
	"strings"
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
func siteDeleteExec(ctx context.Context, a *agent.Agent, j *jobs.Job) (err error) {
	poolRemoved := false
	defer func() {
		var coded *rpc.CodedError
		if poolRemoved && err != nil && !(errors.As(err, &coded) && (coded.Code == "conflict" || coded.Code == "not_found" || coded.Code == "invalid_argument" || coded.Code == "precondition_failed")) {
			err = errors.Join(err, jobs.ErrRetry)
		}
	}()
	if raw, err := a.Store.TLSIntent(ctx); err != nil {
		return err
	} else if len(raw) > 0 {
		return rpc.Errf("precondition_failed", "Unresolved TLS publication")
	}
	var v siteDeleteArgs
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return e
	}
	if e := v.Validate(); e != nil {
		return e
	}
	var durable jobs.Job
	if v.PurgeFiles {
		var e error
		durable, e = a.Store.GetJob(ctx, j.ID)
		if e != nil {
			return jobs.ErrRetry
		}
		poolRemoved = durable.Step == "site_pool_removed"
		if poolRemoved {
			j.Step = durable.Step
		}
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
	if a.Jobs == nil {
		return rpc.Errf("agent_error", "Site locking unavailable")
	}
	unlock, e := a.Jobs.LockSiteOSUser(ctx, s.ID, s.Name)
	if e != nil {
		return e
	}
	defer unlock()
	// Re-read after taking both locks: a queued operation cannot rely on admission state.
	s, e = a.Store.SiteByID(ctx, v.SiteID)
	if errors.Is(e, sql.ErrNoRows) {
		j.Result = json.RawMessage(`{}`)
		return nil
	}
	if e != nil {
		return e
	}
	if s.Docroot != filepath.Join(a.Cfg.SitesRoot, s.Name, "public") {
		return rpc.Errf("precondition_failed", "Unexpected site docroot")
	}
	dbs, e := a.Store.ListDatabases(ctx)
	if e != nil {
		return e
	}
	var attached []string
	for _, db := range dbs {
		if db.SiteID != nil && *db.SiteID == s.ID {
			if !id.Valid(db.ID) {
				return rpc.Errf("agent_error", "Invalid database identifier")
			}
			attached = append(attached, db.ID)
		}
	}
	if len(attached) != 0 {
		j.Result, e = json.Marshal(struct {
			IDs []string `json:"attached_database_ids"`
		}{attached})
		if e != nil {
			return e
		}
		return rpc.Errf("precondition_failed", "Attached databases: %s", strings.Join(attached, ", "))
	}
	wasDeleting := s.Status == "deleting"
	if !wasDeleting {
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
	if s.Type != "static" {
		if a.PHP == nil || a.Exec == nil {
			return rpc.Errf("agent_error", "PHP execution unavailable")
		}
		if !poolRemoved {
			if e = php.VerifyUserForDelete(ctx, a.Exec, a.Cfg.SitesRoot, s.Name, s.ID); e != nil {
				return e
			}
			// Deleting is already committed, so absence is the authoritative projection.
			if wasDeleting || j.Attempts > 1 {
				if e = a.PHP.Recover(ctx, s.Name, php.PoolState{SiteID: s.ID}); e != nil {
					return e
				}
			} else if e = a.PHP.ApplyCommitted(ctx, s.Name, php.PoolState{SiteID: s.ID}, php.PoolState{SiteID: s.ID}); e != nil {
				return e
			}
			if v.PurgeFiles {
				durable.Step = "site_pool_removed"
				if e = a.Store.UpdateJob(ctx, durable); e != nil {
					return jobs.ErrRetry
				}
				poolRemoved = true
			}
		}
		if poolRemoved {
			// The durable step proves this job verified ownership before removal.
			if wasDeleting && j.Step == "site_pool_removed" {
				if e = a.PHP.Recover(ctx, s.Name, php.PoolState{SiteID: s.ID}); e != nil {
					return e
				}
			}
			j.Step = durable.Step
		}
	}
	if !v.PurgeFiles {
		if a.Exec == nil {
			return rpc.Errf("agent_error", "Site account unavailable")
		}
		if e = php.RetainUser(ctx, a.Exec, a.Cfg.SitesRoot, s.Name, s.ID); e != nil {
			return e
		}
	}
	if v.PurgeFiles {
		if e = platform.RemoveSite(ctx, a.Exec, a.Cfg.SitesRoot, a.Cfg.LogDir, s.Name, s.ID); e != nil {
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

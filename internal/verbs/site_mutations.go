package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"path/filepath"
	"strings"
)

type siteUpdateArgs struct {
	SiteID      string  `json:"site_id"`
	PHPVersion  *string `json:"php_version,omitempty"`
	ForceHTTPS  *bool   `json:"force_https,omitempty"`
	HSTSMaxAge  *int64  `json:"hsts_max_age,omitempty"`
	Maintenance *bool   `json:"maintenance,omitempty"`
}

func (v *siteUpdateArgs) Validate() error {
	if e := (&siteIDArgs{v.SiteID}).Validate(); e != nil {
		return e
	}
	if v.PHPVersion != nil {
		return rpc.Errf("unsupported", "Only static sites are available")
	}
	if v.HSTSMaxAge != nil && *v.HSTSMaxAge < 0 {
		return rpc.Errf("invalid_argument", "Invalid HSTS duration")
	}
	return nil
}

type domainAddArgs struct {
	SiteID   string `json:"site_id"`
	FQDN     string `json:"fqdn"`
	DomainID string `json:"domain_id,omitempty"`
}

func (v *domainAddArgs) Validate() error {
	if e := (&siteIDArgs{v.SiteID}).Validate(); e != nil {
		return e
	}
	return (&validate.V{}).Hostname(v.FQDN)
}

type childRemoveArgs struct {
	DomainID   string `json:"domain_id,omitempty"`
	RedirectID string `json:"redirect_id,omitempty"`
	SiteID     string `json:"site_id,omitempty"`
}

func (v *childRemoveArgs) Validate() error {
	if !id.Valid(v.DomainID) && !id.Valid(v.RedirectID) {
		return rpc.Errf("invalid_argument", "Invalid child ID")
	}
	return nil
}

type redirectAddArgs struct {
	SiteID     string `json:"site_id"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	Code       int    `json:"code"`
	RedirectID string `json:"redirect_id,omitempty"`
}

func (v *redirectAddArgs) Validate() error {
	if e := (&siteIDArgs{v.SiteID}).Validate(); e != nil {
		return e
	}
	if e := (&validate.V{}).RedirectSource(v.Source); e != nil {
		return e
	}
	if e := (&validate.V{}).RedirectTarget(v.Target); e != nil {
		return e
	}
	if v.Code != 301 && v.Code != 302 {
		return rpc.Errf("invalid_argument", "Invalid redirect code")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "site.update", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &siteUpdateArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*siteUpdateArgs).SiteID }, Run: siteUpdate, Exec: siteUpdateExec})
	rpc.Register(rpc.Verb{Name: "domain.add", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &domainAddArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*domainAddArgs).SiteID }, Run: domainAdd, Exec: domainAddExec})
	rpc.Register(rpc.Verb{Name: "domain.remove", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &childRemoveArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*childRemoveArgs).SiteID }, Run: domainRemove, Exec: domainRemoveExec})
	rpc.Register(rpc.Verb{Name: "redirect.add", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &redirectAddArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*redirectAddArgs).SiteID }, Run: redirectAdd, Exec: redirectAddExec})
	rpc.Register(rpc.Verb{Name: "redirect.remove", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &childRemoveArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*childRemoveArgs).SiteID }, Run: redirectRemove, Exec: redirectRemoveExec})
}

// The HTTP renderer must never overwrite an installed TLS configuration.
func mutableSite(ctx context.Context, a *agent.Agent, sid string) (store.Site, error) {
	s, e := a.Store.SiteByID(ctx, sid)
	if e != nil {
		return s, siteError(e)
	}
	if s.Status != "active" {
		return s, rpc.Errf("precondition_failed", "Site must be active")
	}
	if s.CertSource != "none" || s.ForceHTTPS || s.HSTSMaxAge != 0 {
		return s, rpc.Errf("precondition_failed", "TLS-aware rendering is required")
	}
	ds, e := a.Store.ListDomains(ctx, sid)
	if e != nil {
		return s, e
	}
	for _, d := range ds {
		if d.CertState != "none" {
			return s, rpc.Errf("precondition_failed", "TLS-aware rendering is required")
		}
	}
	return s, nil
}
func siteUpdate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*siteUpdateArgs)
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return nil, e
	}
	if v.HSTSMaxAge != nil && rpc.CallerFrom(ctx).Role != "owner" {
		return nil, rpc.Errf("forbidden", "Only an owner may set HSTS")
	}
	if v.ForceHTTPS != nil && *v.ForceHTTPS || v.HSTSMaxAge != nil && *v.HSTSMaxAge != 0 {
		return nil, rpc.Errf("precondition_failed", "A valid installed certificate is required")
	}
	return a.Jobs.Enqueue(ctx, "site.update", v, "site:"+v.SiteID)
}
func siteUpdateExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v siteUpdateArgs
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return e
	}
	if e := v.Validate(); e != nil {
		return e
	}
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return e
	}
	if v.ForceHTTPS != nil && *v.ForceHTTPS || v.HSTSMaxAge != nil && *v.HSTSMaxAge != 0 {
		return rpc.Errf("precondition_failed", "A valid installed certificate is required")
	}
	if e := a.Store.UpdateSite(ctx, v.SiteID, store.SitePatch{Maintenance: v.Maintenance, ForceHTTPS: v.ForceHTTPS, HSTSMaxAge: v.HSTSMaxAge}); e != nil {
		return siteError(e)
	}
	return applySite(ctx, a, v.SiteID, j)
}
func domainAdd(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*domainAddArgs)
	if v.DomainID != "" {
		return nil, rpc.Errf("invalid_argument", "Domain ID is agent assigned")
	}
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return nil, e
	}
	v.FQDN = strings.ToLower(v.FQDN)
	var e error
	v.DomainID, e = id.New()
	if e != nil {
		return nil, e
	}
	return a.Jobs.Enqueue(ctx, "domain.add", &v, "site:"+v.SiteID)
}
func domainAddExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v domainAddArgs
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return e
	}
	if e := v.Validate(); e != nil {
		return e
	}
	if !id.Valid(v.DomainID) {
		return rpc.Errf("invalid_argument", "Invalid domain ID")
	}
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return e
	}
	d, e := a.Store.AddDomain(ctx, store.Domain{ID: v.DomainID, SiteID: v.SiteID, FQDN: v.FQDN})
	if e != nil {
		return siteError(e)
	}
	if e = applySite(ctx, a, v.SiteID, j); e != nil {
		return e
	}
	j.Result, e = json.Marshal(map[string]string{"domain_id": d.ID})
	return e
}
func removableDomain(ctx context.Context, a *agent.Agent, d store.Domain) error {
	ds, e := a.Store.ListDomains(ctx, d.SiteID)
	if e != nil {
		return e
	}
	if d.IsPrimary || len(ds) <= 1 {
		return rpc.Errf("precondition_failed", "Cannot remove primary or last domain")
	}
	return nil
}
func domainRemove(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*childRemoveArgs)
	if v.SiteID != "" || v.RedirectID != "" {
		return nil, rpc.Errf("invalid_argument", "Invalid domain removal arguments")
	}
	d, e := a.Store.DomainByID(ctx, v.DomainID)
	if e != nil {
		return nil, siteError(e)
	}
	v.SiteID = d.SiteID
	if _, e = mutableSite(ctx, a, v.SiteID); e != nil {
		return nil, e
	}
	if e = removableDomain(ctx, a, d); e != nil {
		return nil, e
	}
	return a.Jobs.Enqueue(ctx, "domain.remove", &v, "site:"+v.SiteID)
}
func domainRemoveExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v childRemoveArgs
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return e
	}
	if e := v.Validate(); e != nil {
		return e
	}
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return e
	}
	d, e := a.Store.DomainByID(ctx, v.DomainID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil {
		if d.SiteID != v.SiteID {
			return rpc.Errf("precondition_failed", "Domain parent changed")
		}
		if e = removableDomain(ctx, a, d); e != nil {
			return e
		}
		if e = a.Store.RemoveDomain(ctx, d.ID); e != nil {
			return siteError(e)
		}
	}
	return applySite(ctx, a, v.SiteID, j)
}
func redirectAdd(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*redirectAddArgs)
	if v.RedirectID != "" {
		return nil, rpc.Errf("invalid_argument", "Redirect ID is agent assigned")
	}
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return nil, e
	}
	var e error
	v.RedirectID, e = id.New()
	if e != nil {
		return nil, e
	}
	return a.Jobs.Enqueue(ctx, "redirect.add", &v, "site:"+v.SiteID)
}
func redirectAddExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v redirectAddArgs
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return e
	}
	if e := v.Validate(); e != nil {
		return e
	}
	if !id.Valid(v.RedirectID) {
		return rpc.Errf("invalid_argument", "Invalid redirect ID")
	}
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return e
	}
	_, e := a.Store.AddRedirect(ctx, store.Redirect{ID: v.RedirectID, SiteID: v.SiteID, Source: v.Source, Target: v.Target, Code: v.Code})
	if e != nil {
		return siteError(e)
	}
	if e = applySite(ctx, a, v.SiteID, j); e != nil {
		return e
	}
	j.Result, e = json.Marshal(map[string]string{"redirect_id": v.RedirectID})
	return e
}
func redirectRemove(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*childRemoveArgs)
	if v.SiteID != "" || v.DomainID != "" {
		return nil, rpc.Errf("invalid_argument", "Invalid redirect removal arguments")
	}
	r, e := a.Store.RedirectByID(ctx, v.RedirectID)
	if e != nil {
		return nil, siteError(e)
	}
	v.SiteID = r.SiteID
	if _, e = mutableSite(ctx, a, v.SiteID); e != nil {
		return nil, e
	}
	return a.Jobs.Enqueue(ctx, "redirect.remove", &v, "site:"+v.SiteID)
}
func redirectRemoveExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v childRemoveArgs
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return e
	}
	if e := v.Validate(); e != nil {
		return e
	}
	if _, e := mutableSite(ctx, a, v.SiteID); e != nil {
		return e
	}
	r, e := a.Store.RedirectByID(ctx, v.RedirectID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil && r.SiteID != v.SiteID {
		return rpc.Errf("precondition_failed", "Redirect parent changed")
	}
	if e = a.Store.RemoveRedirect(ctx, v.RedirectID); e != nil {
		return e
	}
	return applySite(ctx, a, v.SiteID, j)
}
func applySite(ctx context.Context, a *agent.Agent, sid string, j *jobs.Job) error {
	s, e := mutableSite(ctx, a, sid)
	if e != nil {
		return e
	}
	if a.Nginx == nil {
		return rpc.Errf("agent_error", "Nginx unavailable")
	}
	ds, e := a.Store.ListDomains(ctx, sid)
	if e != nil {
		return e
	}
	rs, e := a.Store.ListRedirects(ctx, sid)
	if e != nil {
		return e
	}
	d := nginx.Desired{Name: s.Name, Docroot: s.Docroot, ACMERoot: filepath.Join(a.Cfg.ACMEWebroot, s.Name), Maintenance: s.Maintenance}
	for _, v := range ds {
		d.Domains = append(d.Domains, v.FQDN)
	}
	for _, r := range rs {
		d.Redirects = append(d.Redirects, nginx.Redirect{Source: r.Source, Target: r.Target, Code: r.Code})
	}
	if e = a.Nginx.Apply(ctx, d); e != nil {
		return e
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

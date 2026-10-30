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
	certs "novapanel/internal/tls"
	"novapanel/internal/validate"
	"path/filepath"
	"strings"
	"time"
)

type siteUpdateArgs struct {
	SiteID          string  `json:"site_id"`
	PrimaryDomainID *string `json:"primary_domain_id,omitempty"`
	PHPVersion      *string `json:"php_version,omitempty"`
	ForceHTTPS      *bool   `json:"force_https,omitempty"`
	HSTSMaxAge      *int64  `json:"hsts_max_age,omitempty"`
	Maintenance     *bool   `json:"maintenance,omitempty"`
}

func (v *siteUpdateArgs) Validate() error {
	if e := (&siteIDArgs{v.SiteID}).Validate(); e != nil {
		return e
	}
	if v.PrimaryDomainID != nil && !id.Valid(*v.PrimaryDomainID) {
		return rpc.Errf("invalid_argument", "Invalid primary domain ID")
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
	rpc.Register(rpc.Verb{Name: "site.update", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &siteUpdateArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*siteUpdateArgs).SiteID }, Run: siteUpdate, Exec: siteUpdateExec, ValidateRecovery: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (tlsStartup{a}).validate(ctx, j) }, Recover: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (tlsStartup{a}).resume(ctx, j) }})
	rpc.Register(rpc.Verb{Name: "domain.add", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &domainAddArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*domainAddArgs).SiteID }, Run: domainAdd, Exec: domainAddExec})
	rpc.Register(rpc.Verb{Name: "domain.remove", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &childRemoveArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*childRemoveArgs).SiteID }, ResolveObjectKey: func(ctx context.Context, a *agent.Agent, args any) (string, error) {
		return (siteMutationState{a}).resolveDomainRemoval(ctx, args)
	}, Run: domainRemove, Exec: domainRemoveExec})
	rpc.Register(rpc.Verb{Name: "redirect.add", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &redirectAddArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*redirectAddArgs).SiteID }, Run: redirectAdd, Exec: redirectAddExec})
	rpc.Register(rpc.Verb{Name: "redirect.remove", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &childRemoveArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*childRemoveArgs).SiteID }, ResolveObjectKey: func(ctx context.Context, a *agent.Agent, args any) (string, error) {
		return (siteMutationState{a}).resolveRedirectRemoval(ctx, args)
	}, Run: redirectRemove, Exec: redirectRemoveExec})
}

// Site mutations retain installed TLS and refuse unresolved publication recovery.
type siteMutationState struct{ a *agent.Agent }

func (m siteMutationState) mutableSite(ctx context.Context, sid string) (store.Site, error) {
	a := m.a
	if raw, err := a.Store.TLSIntent(ctx); err != nil {
		return store.Site{}, err
	} else if len(raw) > 0 {
		return store.Site{}, rpc.Errf("precondition_failed", "Unresolved TLS publication")
	}
	s, e := a.Store.SiteByID(ctx, sid)
	if e != nil {
		return s, siteError(e)
	}
	if s.Status != "active" {
		return s, rpc.Errf("precondition_failed", "Site must be active")
	}
	if s.CertSource != "none" {
		ds, err := a.Store.ListDomains(ctx, sid)
		if err != nil {
			return s, err
		}
		_, primary := sslNames(ds)
		if _, err = nginx.LoadTLS(a.Cfg.TLSDir, primary, tlsStaging(ds), time.UnixMilli(a.Now())); err != nil {
			return s, rpc.Errf("precondition_failed", "A valid installed certificate is required")
		}
	}
	return s, nil
}

func tlsStaging(ds []store.Domain) bool {
	for _, d := range ds {
		if d.CertStaging {
			return true
		}
	}
	return false
}

// Initial enable requires full actual coverage; later uncovered aliases stay HTTP.
func (m siteMutationState) updatePolicy(ctx context.Context, v siteUpdateArgs) error {
	s, err := m.a.Store.SiteByID(ctx, v.SiteID)
	if err != nil {
		return err
	}
	ds, err := m.a.Store.ListDomains(ctx, v.SiteID)
	if err != nil {
		return err
	}
	force, age := s.ForceHTTPS, s.HSTSMaxAge
	if v.ForceHTTPS != nil {
		force = *v.ForceHTTPS
	}
	if v.HSTSMaxAge != nil {
		age = *v.HSTSMaxAge
	}
	if age > 0 && !force {
		return rpc.Errf("precondition_failed", "HSTS requires forced HTTPS")
	}
	if force {
		names, primary := sslNames(ds)
		if s.CertSource == "none" || tlsStaging(ds) {
			return rpc.Errf("precondition_failed", "A production certificate is required")
		}
		if s.ForceHTTPS {
			names = []string{primary}
		}
		if _, err = certs.Load(m.a.Cfg.TLSDir, primary, names, time.UnixMilli(m.a.Now())); err != nil {
			return rpc.Errf("precondition_failed", "A valid covering installed certificate is required")
		}
	}
	return nil
}
func (m siteMutationState) primaryTarget(ctx context.Context, sid string, target *string) error {
	if target == nil {
		return nil
	}
	ds, err := m.a.Store.ListDomains(ctx, sid)
	if err != nil {
		return err
	}
	primaries, found := 0, false
	for _, d := range ds {
		if d.IsPrimary {
			primaries++
		}
		if d.ID == *target {
			found = true
		}
	}
	if len(ds) == 0 || primaries != 1 || !found {
		return rpc.Errf("precondition_failed", "Primary domain must be an existing same-site domain with exactly one current primary")
	}
	site, err := m.a.Store.SiteByID(ctx, sid)
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.IsPrimary && d.ID != *target && site.CertSource != "none" {
			names, primary := sslNames(ds)
			if _, e := certs.Load(m.a.Cfg.TLSDir, primary, names, time.UnixMilli(m.a.Now())); e != nil {
				return rpc.Errf("precondition_failed", "A valid full-domain covering certificate is required")
			}
		}
	}
	return nil
}
func siteUpdate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*siteUpdateArgs)
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
		return nil, e
	}
	if v.HSTSMaxAge != nil && rpc.CallerFrom(ctx).Role != "owner" {
		return nil, rpc.Errf("forbidden", "Only an owner may set HSTS")
	}
	if e := (siteMutationState{a}).updatePolicy(ctx, *v); e != nil {
		return nil, e
	}
	if e := (siteMutationState{a}).primaryTarget(ctx, v.SiteID, v.PrimaryDomainID); e != nil {
		return nil, e
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
	if v.PrimaryDomainID != nil {
		durable, e := a.Store.GetJob(ctx, j.ID)
		if e != nil {
			return e
		}
		if durable.Verb != "site.update" || durable.ObjectKey != "site:"+v.SiteID {
			return rpc.Errf("precondition_failed", "Site job binding changed")
		}
		if durable.Step == "tls_committed" {
			j.Step = durable.Step
			j.Result = durable.Result
			return nil
		}
		raw, e := a.Store.TLSIntent(ctx)
		if e != nil {
			return e
		}
		if len(raw) > 0 {
			return (sslPublication{a}).publication(ctx, v.SiteID, j, false, nil, &v)
		}
		site, e := a.Store.SiteByID(ctx, v.SiteID)
		if e != nil {
			return e
		}
		ds, e := a.Store.ListDomains(ctx, v.SiteID)
		if e != nil {
			return e
		}
		for _, d := range ds {
			if d.IsPrimary && d.ID != *v.PrimaryDomainID && site.CertSource != "none" {
				return (sslPublication{a}).publication(ctx, v.SiteID, j, false, nil, &v)
			}
			if d.IsPrimary && d.ID == *v.PrimaryDomainID && v.Maintenance == nil && v.ForceHTTPS == nil && v.HSTSMaxAge == nil {
				if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
					return e
				}
				if e := (siteMutationState{a}).primaryTarget(ctx, v.SiteID, v.PrimaryDomainID); e != nil {
					return e
				}
				j.Result = json.RawMessage(`{}`)
				return nil
			}
		}
	}
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
		return e
	}
	if e := (siteMutationState{a}).updatePolicy(ctx, v); e != nil {
		return e
	}
	if v.HSTSMaxAge != nil && j.Role != "owner" {
		return rpc.Errf("forbidden", "Only an owner may set HSTS")
	}
	if e := (siteMutationState{a}).primaryTarget(ctx, v.SiteID, v.PrimaryDomainID); e != nil {
		return e
	}
	if e := a.Store.UpdateSite(ctx, v.SiteID, store.SitePatch{PrimaryDomainID: v.PrimaryDomainID, Maintenance: v.Maintenance, ForceHTTPS: v.ForceHTTPS, HSTSMaxAge: v.HSTSMaxAge}); e != nil {
		return siteError(e)
	}
	return (siteMutationState{a}).applySite(ctx, v.SiteID, j)
}
func domainAdd(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*domainAddArgs)
	if v.DomainID != "" {
		return nil, rpc.Errf("invalid_argument", "Domain ID is agent assigned")
	}
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
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
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
		return e
	}
	d, e := a.Store.AddDomain(ctx, store.Domain{ID: v.DomainID, SiteID: v.SiteID, FQDN: v.FQDN})
	if e != nil {
		return siteError(e)
	}
	if e = (siteMutationState{a}).applySite(ctx, v.SiteID, j); e != nil {
		return e
	}
	j.Result, e = json.Marshal(map[string]string{"domain_id": d.ID})
	return e
}
func (m siteMutationState) removableDomain(ctx context.Context, d store.Domain) error {
	a := m.a
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
	if _, e = (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
		return nil, e
	}
	if e = (siteMutationState{a}).removableDomain(ctx, d); e != nil {
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
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
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
		if e = (siteMutationState{a}).removableDomain(ctx, d); e != nil {
			return e
		}
		if e = a.Store.RemoveDomain(ctx, d.ID); e != nil {
			return siteError(e)
		}
	}
	return (siteMutationState{a}).applySite(ctx, v.SiteID, j)
}
func redirectAdd(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*redirectAddArgs)
	if v.RedirectID != "" {
		return nil, rpc.Errf("invalid_argument", "Redirect ID is agent assigned")
	}
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
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
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
		return e
	}
	r, e := a.Store.AddRedirect(ctx, store.Redirect{ID: v.RedirectID, SiteID: v.SiteID, Source: v.Source, Target: v.Target, Code: v.Code})
	if e != nil {
		return siteError(e)
	}
	if e = (siteMutationState{a}).applySite(ctx, v.SiteID, j); e != nil {
		return e
	}
	j.Result, e = json.Marshal(map[string]string{"redirect_id": r.ID})
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
	if _, e = (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
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
	if _, e := (siteMutationState{a}).mutableSite(ctx, v.SiteID); e != nil {
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
	return (siteMutationState{a}).applySite(ctx, v.SiteID, j)
}
func (m siteMutationState) applySite(ctx context.Context, sid string, j *jobs.Job) error {
	a := m.a
	s, e := (siteMutationState{a}).mutableSite(ctx, sid)
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
	d := nginx.Desired{Name: s.Name, Docroot: s.Docroot, ACMERoot: filepath.Join(a.Cfg.ACMEWebroot, s.Name), Maintenance: s.Maintenance, ForceHTTPS: s.ForceHTTPS, HSTSMaxAge: s.HSTSMaxAge}
	if s.CertSource != "none" {
		_, primary := sslNames(ds)
		d.TLS, e = nginx.LoadTLS(a.Cfg.TLSDir, primary, tlsStaging(ds), time.UnixMilli(a.Now()))
		if e != nil {
			return e
		}
	}
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

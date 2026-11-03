package verbs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type siteIDArgs struct {
	SiteID string `json:"site_id"`
}

func (v *siteIDArgs) Validate() error {
	if !id.Valid(v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	return nil
}

type siteSummary struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	PrimaryDomain string  `json:"primary_domain"`
	Status        string  `json:"status"`
	PHPVersion    *string `json:"php_version"`
}
type siteRedirect struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Code   int    `json:"code"`
}

type siteDomain struct {
	ID          string `json:"id"`
	FQDN        string `json:"fqdn"`
	IsPrimary   bool   `json:"is_primary"`
	CertState   string `json:"cert_state"`
	CertStaging bool   `json:"cert_staging"`
	NotAfter    *int64 `json:"not_after"`
}

func init() {
	rpc.Register(rpc.Verb{Name: "site.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &emptyArgs{} }, Run: siteList})
	rpc.Register(rpc.Verb{Name: "site.get", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &siteIDArgs{} }, Run: siteGet})
}
func summarizeSite(v store.Site, domains []store.Domain) siteSummary {
	out := siteSummary{ID: v.ID, Name: v.Name, Type: v.Type, Status: v.Status, PHPVersion: v.PHPVersion}
	for _, d := range domains {
		if d.IsPrimary {
			out.PrimaryDomain = d.FQDN
		}
	}
	return out
}
func siteList(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	sites, e := a.Store.ListSites(ctx)
	if e != nil {
		return nil, e
	}
	out := []siteSummary{}
	for _, v := range sites {
		domains, e := a.Store.ListDomains(ctx, v.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, summarizeSite(v, domains))
	}
	return struct {
		Sites []siteSummary `json:"sites"`
	}{out}, nil
}
func siteGet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v, e := a.Store.SiteByID(ctx, args.(*siteIDArgs).SiteID)
	if e != nil {
		return nil, siteError(e)
	}
	domains, e := a.Store.ListDomains(ctx, v.ID)
	if e != nil {
		return nil, e
	}
	out := []siteDomain{}
	cert := struct {
		State    string `json:"state"`
		NotAfter *int64 `json:"not_after"`
	}{State: "none"}
	for _, d := range domains {
		out = append(out, siteDomain{d.ID, d.FQDN, d.IsPrimary, d.CertState, d.CertStaging, d.CertNotAfter})
		// Report persisted metadata, never infer certificate validity from these fields.
		if d.IsPrimary {
			cert.State = d.CertState
			cert.NotAfter = d.CertNotAfter
		}
	}
	rs, e := a.Store.ListRedirects(ctx, v.ID)
	if e != nil {
		return nil, e
	}
	redirects := []siteRedirect{}
	for _, r := range rs {
		redirects = append(redirects, siteRedirect{r.ID, r.Source, r.Target, r.Code})
	}
	var limits *php.Limits
	if v.Type == "php" || v.Type == "wordpress" {
		persisted, err := a.Store.GetPHPLimits(ctx, v.ID)
		if err == nil {
			limits = &persisted
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		// A creating/failed site can lack committed desired limits. Do not
		// invent defaults or infer state from a pool that may not be published.
	}
	var wordpressDatabaseID string
	if v.Type == "wordpress" {
		wordpressDatabaseID, e = a.Store.WordPressDatabaseID(ctx, v.ID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
	}
	return struct {
		WordPressDatabaseID string         `json:"wordpress_database_id,omitempty"`
		Site                siteSummary    `json:"site"`
		Domains             []siteDomain   `json:"domains"`
		Redirects           []siteRedirect `json:"redirects"`
		Cert                any            `json:"cert"`
		PHPLimits           *php.Limits    `json:"php_limits"`
	}{wordpressDatabaseID, summarizeSite(v, domains), out, redirects, cert, limits}, nil
}
func siteError(e error) error {
	switch {
	case errors.Is(e, sql.ErrNoRows):
		return rpc.Errf("not_found", "Site not found")
	case errors.Is(e, store.ErrSiteInvalid):
		return rpc.Errf("invalid_argument", "Invalid site metadata")
	case errors.Is(e, store.ErrSiteConflict):
		return rpc.Errf("conflict", "Site metadata conflict")
	case errors.Is(e, store.ErrSitePrecondition):
		return rpc.Errf("precondition_failed", "Site precondition failed")
	}
	return e
}

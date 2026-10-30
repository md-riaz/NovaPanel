package store

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
)

func TestSiteCreateRejectsDifferentPHPVersion(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	php := "8.3"
	v := Site{ID: "s", Name: "alpha", Type: "php", Docroot: "/srv/sites/alpha/public", PHPVersion: &php}
	d := Domain{ID: "d", FQDN: "alpha.test"}
	if _, e = s.CreateSite(ctx, v, d); e != nil {
		t.Fatal(e)
	}
	php = "8.4"
	if _, e = s.CreateSite(ctx, v, d); !errors.Is(e, ErrSiteConflict) {
		t.Fatal("different PHP version accepted", e)
	}
}

func TestSiteCreateConcurrentEnsure(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.CreateSite(ctx, Site{ID: "s", Name: "alpha", Type: "static", Docroot: "/srv/sites/alpha/public"}, Domain{ID: "d", FQDN: "alpha.test"})
			if e != nil || v.ID != "s" {
				t.Errorf("create %+v %v", v, e)
			}
		}()
	}
	wg.Wait()
	sites, e := s.ListSites(ctx)
	if e != nil || len(sites) != 1 {
		t.Fatalf("sites %+v %v", sites, e)
	}
}

func TestSiteMetadataMutations(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	site, e := s.CreateSite(ctx, Site{ID: "s", Name: "alpha", Type: "static", Docroot: "/srv/sites/alpha/public"}, Domain{ID: "d", FQDN: "alpha.test"})
	if e != nil {
		t.Fatal(e)
	}
	yes := true
	age := int64(300)
	hash := "hash"
	if e = s.UpdateSite(ctx, site.ID, SitePatch{Maintenance: &yes, SSLAuto: &yes, ForceHTTPS: &yes, HSTSMaxAge: &age, ConfigHash: &hash}); e != nil {
		t.Fatal(e)
	}
	got, e := s.SiteByID(ctx, site.ID)
	if e != nil || !got.Maintenance || !got.ForceHTTPS || got.HSTSMaxAge != 300 || got.ConfigHash == nil || *got.ConfigHash != "hash" {
		t.Fatalf("patch: %+v %v", got, e)
	}
	d, e := s.AddDomain(ctx, Domain{ID: "alias", SiteID: site.ID, FQDN: "alias.test"})
	if e != nil || d.CertState != "none" || d.IsPrimary {
		t.Fatalf("domain: %+v %v", d, e)
	}
	d2, e := s.AddDomain(ctx, Domain{ID: "retry", SiteID: site.ID, FQDN: "alias.test"})
	if e != nil || d2.ID != d.ID {
		t.Fatalf("retry %+v %v", d2, e)
	}
	if e = s.RemoveDomain(ctx, "d"); !errors.Is(e, ErrSitePrecondition) {
		t.Fatal("removed primary", e)
	}
	if e = s.RemoveDomain(ctx, "alias"); e != nil {
		t.Fatal(e)
	}
	if e = s.RemoveDomain(ctx, "alias"); e != nil {
		t.Fatal("remove not idempotent", e)
	}
	if e = s.RemoveDomain(ctx, "d"); !errors.Is(e, ErrSitePrecondition) {
		t.Fatal("removed last", e)
	}
	r, e := s.AddRedirect(ctx, Redirect{ID: "r", SiteID: site.ID, Source: "/old", Target: "/new", Code: 301})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RedirectByID(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.RemoveRedirect(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.RemoveRedirect(ctx, r.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AddRedirect(ctx, Redirect{ID: "r", SiteID: site.ID, Source: "/old", Target: "/new", Code: 301}); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteSite(ctx, site.ID); !errors.Is(e, ErrSitePrecondition) {
		t.Fatal("deleted before external cleanup", e)
	}
	if e = s.SetSiteStatus(ctx, site.ID, "deleting"); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteSite(ctx, site.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteSite(ctx, site.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DomainByID(ctx, "d"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("domain not cascaded", e)
	}
	if _, e = s.RedirectByID(ctx, "r"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("redirect not cascaded", e)
	}
	if e = s.UpdateSite(ctx, "missing", SitePatch{}); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
}

package store

import (
	"context"
	"errors"
	"testing"
)

func TestPrimaryPromotion(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_, err = s.CreateSite(ctx, Site{ID: "s", Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, Domain{ID: "d", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetSiteStatus(ctx, "s", "active")
	s.AddDomain(ctx, Domain{ID: "alias", SiteID: "s", FQDN: "alias.test"})
	target := "alias"
	if err = s.UpdateSite(ctx, "s", SitePatch{PrimaryDomainID: &target}); err != nil {
		t.Fatal(err)
	}
	ds, _ := s.ListDomains(ctx, "s")
	for _, d := range ds {
		if d.IsPrimary != (d.ID == target) {
			t.Fatalf("wrong primary %+v", ds)
		}
	}
	before, _ := s.SiteByID(ctx, "s")
	if err = s.UpdateSite(ctx, "s", SitePatch{PrimaryDomainID: &target}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.SiteByID(ctx, "s")
	if before.Updated != after.Updated {
		t.Fatal("same-primary changed timestamp")
	}
	for _, mode := range []string{"missing", "inactive", "tls", "zero", "multiple", "empty"} {
		t.Run(mode, func(t *testing.T) {
			s.SetSiteStatus(ctx, "s", "active")
			s.db.ExecContext(ctx, "UPDATE sites SET cert_source='none' WHERE id='s'")
			s.db.ExecContext(ctx, "UPDATE domains SET is_primary=(id='alias') WHERE site_id='s'")
			id := target
			switch mode {
			case "missing":
				id = "other"
			case "inactive":
				s.SetSiteStatus(ctx, "s", "deleting")
			case "tls":
				s.db.ExecContext(ctx, "UPDATE sites SET cert_source='manual' WHERE id='s'")
			case "zero":
				s.db.ExecContext(ctx, "UPDATE domains SET is_primary=0")
			case "multiple":
				s.db.ExecContext(ctx, "UPDATE domains SET is_primary=1")
			case "empty":
				s.db.ExecContext(ctx, "DELETE FROM domains")
			}
			if err := s.UpdateSite(ctx, "s", SitePatch{PrimaryDomainID: &id}); !errors.Is(err, ErrSitePrecondition) {
				t.Fatalf("accepted %s: %v", mode, err)
			}
		})
	}
}

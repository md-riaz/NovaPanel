//go:build lab && linux

package jobs

import (
	"context"
	"os"
	"testing"

	"novapanel/internal/id"
	"novapanel/internal/platform"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

func TestWordPressLifetimeDomainLab(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires disposable root cgroup lab")
	}
	site, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	d, err := platform.CreateInstallDomain(site)
	if err != nil {
		t.Fatal(err)
	}
	binding := configcustody.ConfigBinding{SiteID: site, UID: 65534, GID: 65534}
	identity := store.SiteLifetime{SiteID: site, UID: 65534, GID: 65534}
	plan := fakeLifetimePlan()
	for i := range plan.Steps {
		plan.Steps[i].Program = "/usr/bin/true"
	}
	activated := false
	l := &WordPressLifetime{domain: d, binding: binding, identity: identity, plan: plan, check: func(context.Context) error { return nil }, activate: func(context.Context, store.SiteLifetime) error { activated = true; return nil }}
	err = withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
		for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums} {
			if _, _, _, err := l.runReviewed(context.Background(), reviewedInstallChild{kind: kind}); err != nil {
				return err
			}
		}
		if err := l.ConfigIsolation(context.Background(), binding, func(context.Context) error { return nil }); err != nil {
			return err
		}
		_, _, _, err := l.runReviewed(context.Background(), reviewedInstallChild{kind: wordpress.InstallCore})
		return err
	}, func(l *WordPressLifetime) error { return nil })
	if err != nil || !activated {
		t.Fatalf("lifetime: %v activated=%v", err, activated)
	}
}

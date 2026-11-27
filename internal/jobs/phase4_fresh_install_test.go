package jobs

import (
	"context"
	"testing"

	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

func TestFreshInstallSequence(t *testing.T) {
	d := &fakeInstallLifetime{held: true, empty: true}
	b := configcustody.ConfigBinding{JobID: "job", SiteID: "site", UID: 123, GID: 456}
	plan := fakeLifetimePlan()
	for i := range plan.Steps {
		if plan.Steps[i].Kind == wordpress.InstallCore {
			plan.Steps[i].Stdin = wordpress.AdministratorPassword
		}
	}
	l := &WordPressLifetime{domain: d, binding: b, identity: store.SiteLifetime{SiteID: "site", UID: 123, GID: 456}, plan: plan, check: func(context.Context) error { return nil }, activate: func(context.Context, store.SiteLifetime) error { return nil }}
	published, released := false, false
	err := withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
		return runFreshInstall(context.Background(), l, func() error {
			return l.ConfigIsolation(context.Background(), b, func(context.Context) error { published = true; return nil })
		}, "admin-password")
	}, func(*WordPressLifetime) error { released = true; return nil })
	if err != nil || !published || !released || d.runs != 3 {
		t.Fatalf("sequence failed: %v", err)
	}
}

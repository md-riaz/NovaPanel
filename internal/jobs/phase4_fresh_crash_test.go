package jobs

import (
	"context"
	"errors"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

type crashInstallDomain struct {
	stop             wordpress.StepKind
	fired, installed bool
	runs             map[wordpress.StepKind]int
}

func (d *crashInstallDomain) WithEmpty(ctx context.Context, f func(context.Context) error) error {
	return f(ctx)
}
func (d *crashInstallDomain) Close(context.Context) error { return nil }
func (d *crashInstallDomain) Run(_ context.Context, _ string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	kind := wordpress.StepKind(args[0])
	d.runs[kind]++
	if kind == wordpress.IsInstalled && !d.installed {
		return nil, nil, 1, nil
	}
	if kind == wordpress.InstallCore {
		if opts.Stdin == nil {
			return nil, nil, -1, errors.New("password missing")
		}
		d.installed = true
	}
	if kind == d.stop && !d.fired {
		d.fired = true
		return nil, nil, -1, errors.New("crash")
	}
	return nil, nil, 0, nil
}

// Exercise the held-lifetime sequence independently of the platform cgroup.
func TestFreshInstallChildCrashReplay(t *testing.T) {
	for _, stop := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums, wordpress.InstallCore} {
		t.Run(string(stop), func(t *testing.T) {
			d := &crashInstallDomain{stop: stop, runs: map[wordpress.StepKind]int{}}
			binding := configcustody.ConfigBinding{JobID: "job", SiteID: "site", UID: 123, GID: 456}
			plan := wordpress.Plan{}
			for _, k := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums, wordpress.IsInstalled, wordpress.InstallCore} {
				step := wordpress.Step{Kind: k, Program: "/stub", Args: []string{string(k)}}
				if k == wordpress.InstallCore {
					step.Stdin = wordpress.AdministratorPassword
				}
				plan.Steps = append(plan.Steps, step)
			}
			published, served := false, false
			run := func(ready bool) error {
				l := &WordPressLifetime{domain: d, binding: binding, identity: store.SiteLifetime{UID: 123, GID: 456}, plan: plan, check: func(context.Context) error { return nil }, activate: func(context.Context, store.SiteLifetime) error { return nil }}
				return withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
					return runFreshInstall(context.Background(), l, ready, func() error { return nil }, func() error {
						return l.ConfigIsolation(context.Background(), binding, func(context.Context) error { published = true; return nil })
					}, "admin")
				}, func(*WordPressLifetime) error { served = true; return nil })
			}
			if err := run(false); err == nil || served {
				t.Fatalf("first attempt: %v", err)
			}
			if err := run(published); err != nil || !served || !d.installed {
				t.Fatalf("replay: %v", err)
			}
			if stop == wordpress.InstallCore && d.runs[wordpress.InstallCore] != 1 {
				t.Fatal("reinstalled")
			}
		})
	}
}

func TestFreshInstallRefusesChildWhenPreflightFails(t *testing.T) {
	d := &crashInstallDomain{runs: map[wordpress.StepKind]int{}}
	plan := wordpress.Plan{Steps: []wordpress.Step{{Kind: wordpress.Download, Program: "/stub", Args: []string{string(wordpress.Download)}}}}
	l := &WordPressLifetime{domain: d, identity: store.SiteLifetime{UID: 123, GID: 456}, plan: plan, check: func(context.Context) error { return errors.New("preflight failed") }}
	err := withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
		_, _, _, err := l.runReviewed(context.Background(), reviewedInstallChild{kind: wordpress.Download})
		return err
	}, func(*WordPressLifetime) error { t.Fatal("released"); return nil })
	if err == nil || len(d.runs) != 0 {
		t.Fatal("child ran after preflight failure")
	}
}

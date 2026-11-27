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
	failureCode      int
	failureStderr    string
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
		code := d.failureCode
		if code == 0 {
			code = -1
		}
		return nil, []byte(d.failureStderr), code, errors.New("child failed")
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
			if stop == wordpress.Download && (d.runs[wordpress.Download] != 2 || d.runs[wordpress.VerifyChecksums] != 1) {
				t.Fatalf("download replay: %#v", d.runs)
			}
			if stop == wordpress.VerifyChecksums && (d.runs[wordpress.Download] != 2 || d.runs[wordpress.VerifyChecksums] != 2) {
				t.Fatalf("checksum replay: %#v", d.runs)
			}
			if stop == wordpress.InstallCore && (d.runs[wordpress.InstallCore] != 1 || d.runs[wordpress.VerifyChecksums] != 2) {
				t.Fatalf("replay must reverify and skip reinstall: %#v", d.runs)
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

func TestFreshArtifactFailureWorkerRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		step   wordpress.StepKind
		code   int
		stderr string
		retry  bool
	}{
		{"download-transport", wordpress.Download, wordpress.ExtractRetryExit, "", true},
		{"checksum-fetch", wordpress.VerifyChecksums, 1, "Error: Couldn't get checksums from WordPress.org.\n", true},
		{"checksum-mismatch", wordpress.VerifyChecksums, 1, "Error: WordPress installation doesn't verify against checksums.\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, s, _, ctx := fixture(t)
			en, err := q.Enqueue(ctx, "site.create", map[string]string{"site_id": "fixture"}, "site:fixture")
			if err != nil {
				t.Fatal(err)
			}
			d := &crashInstallDomain{stop: tc.step, failureCode: tc.code, failureStderr: tc.stderr, runs: map[wordpress.StepKind]int{}}
			binding := configcustody.ConfigBinding{JobID: en.Job.ID, SiteID: "site", UID: 123, GID: 456}
			plan := wordpress.Plan{}
			for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums, wordpress.IsInstalled, wordpress.InstallCore} {
				step := wordpress.Step{Kind: kind, Program: "/stub", Args: []string{string(kind)}}
				if kind == wordpress.InstallCore {
					step.Stdin = wordpress.AdministratorPassword
				}
				plan.Steps = append(plan.Steps, step)
			}
			served := false
			q.SetRunner(func(run context.Context, _ *Job) error {
				l := &WordPressLifetime{domain: d, binding: binding, identity: store.SiteLifetime{UID: 123, GID: 456}, plan: plan, check: func(context.Context) error { return nil }, activate: func(context.Context, store.SiteLifetime) error { return nil }}
				return withInstallLifetime(run, l, func(l *WordPressLifetime) error {
					return runFreshInstall(run, l, false, func() error { return nil }, func() error { return l.ConfigIsolation(run, binding, func(context.Context) error { return nil }) }, "admin")
				}, func(*WordPressLifetime) error { served = true; return nil })
			})
			if err = q.execute(ctx, en.Job); err != nil {
				t.Fatal(err)
			}
			job, err := s.GetJob(ctx, en.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.retry {
				if job.State != "failed" || served {
					t.Fatalf("mismatch not failed closed: %+v", job)
				}
				return
			}
			if job.State != "queued" || served {
				t.Fatalf("not retried: %+v", job)
			}
			if err = q.execute(ctx, job); err != nil {
				t.Fatal(err)
			}
			job, err = s.GetJob(ctx, en.Job.ID)
			if err != nil || job.State != "done" || !served {
				t.Fatalf("retry did not converge: %+v %v", job, err)
			}
		})
	}
}

func TestFreshInstallRecoveryRechecksChecksumMismatch(t *testing.T) {
	d := &crashInstallDomain{stop: wordpress.VerifyChecksums, runs: map[wordpress.StepKind]int{}}
	plan := wordpress.Plan{Steps: []wordpress.Step{{Kind: wordpress.VerifyChecksums, Program: "/stub", Args: []string{string(wordpress.VerifyChecksums)}}}}
	pins := 0
	for attempt := 0; attempt < 2; attempt++ {
		d.fired = false
		l := &WordPressLifetime{domain: d, identity: store.SiteLifetime{UID: 123, GID: 456}, plan: plan, check: func(context.Context) error { return nil }}
		err := withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
			return runFreshInstall(context.Background(), l, true, func() error { pins++; return nil }, func() error { t.Fatal("published after mismatch"); return nil }, "admin")
		}, func(*WordPressLifetime) error { t.Fatal("served after mismatch"); return nil })
		if err == nil || errors.Is(err, ErrRetry) {
			t.Fatalf("checksum mismatch must fail terminally: %v", err)
		}
	}
	if pins != 2 || d.runs[wordpress.VerifyChecksums] != 2 || d.runs[wordpress.InstallCore] != 0 {
		t.Fatalf("recovery did not fail closed: pins=%d runs=%v", pins, d.runs)
	}
}

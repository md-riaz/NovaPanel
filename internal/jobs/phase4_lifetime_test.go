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

type fakeInstallLifetime struct {
	held, empty, closed bool
	closeErr            error
	runs                int
}

func (d *fakeInstallLifetime) WithEmpty(ctx context.Context, fn func(context.Context) error) error {
	if !d.held || !d.empty || d.closed {
		return errors.New("domain not held empty")
	}
	return fn(ctx)
}
func (d *fakeInstallLifetime) Run(ctx context.Context, _ string, _ []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	if !d.held || !d.empty || d.closed || o.Uid != 123 || o.Gid != 456 || len(o.Groups) != 0 {
		return nil, nil, -1, errors.New("unsafe admission")
	}
	d.runs++
	return nil, nil, 0, nil
}
func (d *fakeInstallLifetime) Close(context.Context) error { d.closed = true; return d.closeErr }

func fakeLifetimePlan() wordpress.Plan {
	return wordpress.Plan{Steps: []wordpress.Step{{Kind: wordpress.Download, Program: "/stub"}, {Kind: wordpress.VerifyChecksums, Program: "/stub"}, {Kind: wordpress.CreateConfig, Program: "/stub"}, {Kind: wordpress.InstallCore, Program: "/stub"}}}
}
func TestHeldLifetimeIsolationAndRelease(t *testing.T) {
	for _, mode := range []string{"success", "callback", "close", "early", "duplicate", "panic"} {
		t.Run(mode, func(t *testing.T) {
			domain := &fakeInstallLifetime{held: true, empty: true}
			if mode == "close" {
				domain.closeErr = errors.New("cannot confirm empty")
			}
			binding := configcustody.ConfigBinding{JobID: "job", SiteID: "site", UID: 123, GID: 456}
			identity := store.SiteLifetime{SiteID: "site", UID: 123, GID: 456}
			activated, effects, releases := 0, 0, 0
			l := &WordPressLifetime{domain: domain, binding: binding, identity: identity, plan: fakeLifetimePlan(), check: func(context.Context) error { return nil }, activate: func(_ context.Context, got store.SiteLifetime) error {
				if !domain.closed || got != identity {
					t.Fatal("activation before drain or with changed identity")
				}
				activated++
				return nil
			}}
			err := withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
				if mode == "early" {
					return l.ReleaseServing(context.Background())
				}
				if mode == "panic" {
					panic("fixture")
				}
				for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums, wordpress.CreateConfig} {
					if _, _, _, err := l.runReviewed(context.Background(), reviewedInstallChild{kind: kind}); err != nil {
						return err
					}
				}
				err := l.ConfigIsolation(context.Background(), binding, func(context.Context) error {
					if !domain.held || !domain.empty || domain.closed {
						t.Fatal("effect outside held empty domain")
					}
					effects++
					if mode == "callback" {
						return errors.New("effect failed")
					}
					return nil
				})
				if err != nil {
					return err
				}
				if mode == "duplicate" {
					_ = l.ConfigIsolation(context.Background(), binding, func(context.Context) error { effects++; return nil })
				}
				_, _, _, err = l.runReviewed(context.Background(), reviewedInstallChild{kind: wordpress.InstallCore})
				return err
			}, func(l *WordPressLifetime) error { releases++; return l.ReleaseServing(context.Background()) })
			if !domain.closed {
				t.Fatal("domain not closed")
			}
			if mode == "success" {
				if err != nil || effects != 1 || releases != 1 || activated != 1 || domain.runs != 4 {
					t.Fatalf("success: %v effects=%d release=%d activation=%d runs=%d", err, effects, releases, activated, domain.runs)
				}
			} else if err == nil || releases != 0 || activated != 0 {
				t.Fatalf("failure released: %v release=%d activation=%d", err, releases, activated)
			}
			if effects > 1 {
				t.Fatal("callback executed twice")
			}
			if err := l.ReleaseServing(context.Background()); err == nil {
				t.Fatal("release after lifetime")
			}
		})
	}
}

// Returning with a retained child must cancel and drain it before releasing exclusion.
type blockingInstallLifetime struct {
	started chan struct{}
	done    chan struct{}
	closed  bool
}

func (d *blockingInstallLifetime) WithEmpty(context.Context, func(context.Context) error) error {
	return nil
}
func (d *blockingInstallLifetime) Run(ctx context.Context, _ string, _ []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	close(d.started)
	<-ctx.Done()
	close(d.done)
	return nil, nil, -1, ctx.Err()
}
func (d *blockingInstallLifetime) Close(context.Context) error {
	select {
	case <-d.done:
	default:
		return errors.New("child survived teardown")
	}
	d.closed = true
	return nil
}
func TestLifetimeReturnCancelsRetainedChild(t *testing.T) {
	d := &blockingInstallLifetime{started: make(chan struct{}), done: make(chan struct{})}
	activated := false
	l := &WordPressLifetime{domain: d, identity: store.SiteLifetime{UID: 123, GID: 456}, plan: fakeLifetimePlan(), check: func(context.Context) error { return nil }, activate: func(context.Context, store.SiteLifetime) error { activated = true; return nil }}
	err := withInstallLifetime(context.Background(), l, func(l *WordPressLifetime) error {
		go func() {
			_, _, _, _ = l.runReviewed(context.Background(), reviewedInstallChild{kind: wordpress.Download})
		}()
		<-d.started
		return nil
	}, func(l *WordPressLifetime) error { return l.ReleaseServing(context.Background()) })
	if err == nil || !d.closed || activated {
		t.Fatalf("retained child: %v closed=%v activated=%v", err, d.closed, activated)
	}
}

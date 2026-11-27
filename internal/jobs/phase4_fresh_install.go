package jobs

import (
	"context"
	"errors"
	"os"
	"strings"

	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

// runFreshInstall admits only fixed reviewed children while config custody is held.
func runFreshInstall(ctx context.Context, lifetime *WordPressLifetime, publish func() error, adminPassword string) error {
	if lifetime == nil || publish == nil || adminPassword == "" {
		return errors.New("jobs: WordPress install unavailable")
	}
	for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums} {
		if _, _, code, err := lifetime.runReviewed(ctx, reviewedInstallChild{kind: kind}); err != nil || code != 0 {
			return errors.New("jobs: WordPress artifact step failed")
		}
	}
	if err := publish(); err != nil {
		return err
	}
	if _, _, code, err := lifetime.runReviewed(ctx, reviewedInstallChild{kind: wordpress.InstallCore, options: runner.RunOpts{Stdin: strings.NewReader(adminPassword + "\n")}}); err != nil || code != 0 {
		return errors.New("jobs: WordPress core installation failed")
	}
	return nil
}

// InstallFreshWordPress holds the fresh-account lifetime through serving release.
func (q *Queue) InstallFreshWordPress(ctx context.Context, jobID string, identity store.SiteLifetime, release func() error) error {
	// shortcut: archive pinning and crash replay are not wired, remove this refusal after both are enforced.
	if !freshWordPressAcquisitionReady() {
		return rpcerr.Errf("unsupported", "WordPress acquisition is unavailable")
	}
	s, ok := q.store.(interface {
		operationStore
		CheckpointOperation(context.Context, string, string, string) error
	})
	if !ok || release == nil {
		return errors.New("jobs: WordPress install unavailable")
	}
	return q.WithFreshWordPressLifetime(ctx, jobID, identity, func(l *WordPressLifetime) error {
		op, err := s.LoadOperation(ctx, jobID)
		if err != nil {
			return err
		}
		if op.Checkpoint != "db_applied" {
			return rpcerr.Errf("precondition_failed", "WordPress install requires reconciliation")
		}
		entries, err := os.ReadDir(identity.Docroot)
		if err != nil {
			return rpcerr.Errf("agent_error", "WordPress docroot unavailable")
		}
		if len(entries) != 0 {
			return rpcerr.Errf("precondition_failed", "WordPress docroot is not empty")
		}
		credentials, err := q.OpenWordPressCredentials(ctx, jobID)
		if err != nil {
			return err
		}
		if err = runFreshInstall(ctx, l, func() error { return q.CreateWordPressConfig(ctx, jobID, q.configCustody, l.ConfigIsolation) }, credentials.AdminPassword); err != nil {
			return err
		}
		return s.CheckpointOperation(ctx, jobID, "config_published", "verified")
	}, func(*WordPressLifetime) error { return release() })
}

func freshWordPressAcquisitionReady() bool { return false }

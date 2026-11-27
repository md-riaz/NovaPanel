package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	runner "novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

// runFreshInstall admits only fixed reviewed children while config custody is held.
func runFreshInstall(ctx context.Context, lifetime *WordPressLifetime, artifactsReady bool, verifyArchive, publish func() error, adminPassword string) error {
	if lifetime == nil || verifyArchive == nil || publish == nil || adminPassword == "" {
		return errors.New("jobs: WordPress install unavailable")
	}
	if artifactsReady {
		lifetime.mu.Lock()
		if lifetime.closed || lifetime.failed || lifetime.stage != 0 {
			lifetime.mu.Unlock()
			return errors.New("jobs: WordPress artifact replay refused")
		}
		lifetime.stage = 2
		lifetime.mu.Unlock()
	} else {
		for _, kind := range []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums} {
			if _, _, code, err := lifetime.runReviewed(ctx, reviewedInstallChild{kind: kind}); err != nil || code != 0 {
				return errors.New("jobs: WordPress artifact step failed")
			}
			if kind == wordpress.Download {
				if err := verifyArchive(); err != nil {
					return err
				}
			}
		}
	}
	if err := publish(); err != nil {
		return err
	}
	_, _, installed, err := lifetime.runReviewed(ctx, reviewedInstallChild{kind: wordpress.IsInstalled})
	if err != nil || (installed != 0 && installed != 1) {
		return errors.New("jobs: WordPress installation check failed")
	}
	if installed == 0 {
		return nil
	}
	if _, _, code, err := lifetime.runReviewed(ctx, reviewedInstallChild{kind: wordpress.InstallCore, options: runner.RunOpts{Stdin: strings.NewReader(adminPassword + "\n")}}); err != nil || code != 0 {
		return errors.New("jobs: WordPress core installation failed")
	}
	return nil
}

// InstallFreshWordPress holds the fresh-account lifetime through serving release.
func (q *Queue) InstallFreshWordPress(ctx context.Context, jobID string, identity store.SiteLifetime, release func() error) error {
	s, ok := q.store.(interface {
		operationStore
		CheckpointOperation(context.Context, string, string, string) error
		MarkWordPressDownloadStarted(context.Context, string) error
	})
	if !ok || release == nil {
		return errors.New("jobs: WordPress install unavailable")
	}
	return q.WithFreshWordPressLifetime(ctx, jobID, identity, func(l *WordPressLifetime) error {
		op, err := s.LoadOperation(ctx, jobID)
		if err != nil {
			return err
		}
		if op.Checkpoint != "db_applied" && op.Checkpoint != "config_published" && op.Checkpoint != "verified" {
			return rpcerr.Errf("precondition_failed", "WordPress install requires reconciliation")
		}
		if !op.Identity.DownloadStarted {
			entries, err := os.ReadDir(identity.Docroot)
			if err != nil {
				return rpcerr.Errf("agent_error", "WordPress docroot unavailable")
			}
			if len(entries) != 0 {
				return rpcerr.Errf("precondition_failed", "WordPress docroot is not empty")
			}
			if err := s.MarkWordPressDownloadStarted(ctx, jobID); err != nil {
				return err
			}
		}
		credentials, err := q.OpenWordPressCredentials(ctx, jobID)
		if err != nil {
			return err
		}
		manifest := wordpress.ApprovedArtifacts()
		archive := filepath.Join(filepath.Dir(l.plan.Dir), "cache", "core", "wordpress-"+manifest.WordPress.Version+"-"+manifest.Locale+".tar.gz")
		archivePresent := false
		if _, err := os.Lstat(archive); err == nil {
			if err := wordpress.VerifyDownloadedArchive(archive, identity.UID); err != nil {
				return err
			}
			archivePresent = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		artifactsReady := op.Checkpoint != "db_applied" && archivePresent
		if !artifactsReady && op.Identity.DownloadStarted {
			for i := range l.plan.Steps {
				if l.plan.Steps[i].Kind == wordpress.Download {
					l.plan.Steps[i].Args = append(l.plan.Steps[i].Args, "--force")
				}
			}
		}
		if err = runFreshInstall(ctx, l, artifactsReady, func() error { return wordpress.VerifyDownloadedArchive(archive, identity.UID) }, func() error { return q.CreateWordPressConfig(ctx, jobID, q.configCustody, l.ConfigIsolation) }, credentials.AdminPassword); err != nil {
			return err
		}
		if op.Checkpoint != "verified" {
			return s.CheckpointOperation(ctx, jobID, "config_published", "verified")
		}
		return nil
	}, func(*WordPressLifetime) error { return release() })
}

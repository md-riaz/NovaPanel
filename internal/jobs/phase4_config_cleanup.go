package jobs

import (
	"context"
	"errors"

	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

// SetConfigCustody configures a trusted root before startup. There is no default
// production path. The caller must exclude concurrent root writers/relocation.
func (q *Queue) SetConfigCustody(root string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.running {
		panic("jobs: custody configuration while running")
	}
	q.configCustody = root
}

func (q *Queue) reconcileConfigRecord(ctx context.Context, jid string) error {
	q.mu.Lock()
	root := q.configCustody
	q.mu.Unlock()
	if root == "" {
		return nil
	}
	s, ok := q.store.(interface {
		LoadOperation(context.Context, string) (store.Operation, error)
		CheckTerminalWordPressConfig(context.Context, string) error
		AuthorizeTerminalWordPressConfig(context.Context, string) error
		AcknowledgeTerminalWordPressConfig(context.Context, string) error
	})
	seal, hasSeal := q.sealer.(*secret.Sealer)
	if !ok || !hasSeal {
		return errors.New("jobs: config terminal reconciliation unavailable")
	}
	op, err := s.LoadOperation(ctx, jid)
	if err != nil {
		return err
	}
	if op.Kind != store.WordPressInstall || op.Checkpoint != "done" {
		return nil
	}
	if err = q.healthy(); err != nil {
		return err
	}
	if err = s.CheckTerminalWordPressConfig(ctx, jid); err != nil {
		return errors.New("jobs: config terminal reconciliation refused")
	}
	if op.Identity.ConfigCleanup == "acknowledged" {
		return configcustody.CheckConfigRecordAbsent(ctx, root, jid)
	}
	w := op.Identity.WordPress
	plan, err := wordpress.BuildInstall(w.Input, w.Evidence)
	if err != nil {
		return errors.New("jobs: config terminal reconciliation refused")
	}
	b := configcustody.ConfigBinding{JobID: jid, SiteID: op.SiteID, DatabaseID: op.DatabaseID, AccountID: op.Identity.Database.PrimaryUserID, Docroot: plan.Docroot, UID: w.Input.UID, GID: w.Input.GID}
	if err = configcustody.RemoveConfigRecordAuthorized(ctx, root, b, seal, func() error {
		return s.AuthorizeTerminalWordPressConfig(ctx, jid)
	}); err != nil {
		return err
	}
	return s.AcknowledgeTerminalWordPressConfig(ctx, jid)
}

func (q *Queue) reconcileConfigRecords(ctx context.Context) error {
	q.mu.Lock()
	root := q.configCustody
	q.mu.Unlock()
	if root == "" {
		return nil
	}
	s, ok := q.store.(interface {
		TerminalWordPressConfigs(context.Context) ([]string, error)
	})
	if !ok {
		return ErrRecoveryBlocked
	}
	ids, err := s.TerminalWordPressConfigs(ctx)
	if err != nil {
		return err
	}
	for _, jid := range ids {
		op, err := q.store.(operationStore).LoadOperation(ctx, jid)
		if err != nil {
			return err
		}
		release, err := q.locks.lockSetContext(ctx, op.Keys())
		if err != nil {
			return err
		}
		err = q.reconcileConfigRecord(ctx, jid)
		release()
		if err != nil {
			return err
		}
	}
	return nil
}

// CleanupFreshWordPressRollbackConfig authenticates the sealed record before
// removal after the site-bound external teardown checkpoint is durable.
func (q *Queue) CleanupFreshWordPressRollbackConfig(ctx context.Context, jid string) error {
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	if lease == nil || lease.queue != q || lease.job != jid || !lease.active.Load() {
		return ErrRecoveryBlocked
	}
	s, ok := q.store.(interface {
		LoadOperation(context.Context, string) (store.Operation, error)
		LoadSiteLifetime(context.Context, string) (store.SiteLifetime, error)
		CheckFreshSiteLifetime(context.Context, store.SiteLifetime) error
	})
	seal, hasSeal := q.sealer.(*secret.Sealer)
	if !ok || !hasSeal {
		return ErrRecoveryBlocked
	}
	op, err := s.LoadOperation(ctx, jid)
	if err != nil || op.Kind != store.WordPressInstall || op.Identity.WordPress == nil {
		return ErrRecoveryBlocked
	}
	held, _ := ctx.Value(heldLocksKey{}).(map[string]bool)
	for _, key := range op.Keys() {
		if !held[key] {
			return ErrRecoveryBlocked
		}
	}
	j, err := q.store.GetJob(ctx, jid)
	if err != nil || j.State != "running" || j.Step != store.FreshWordPressRollbackCleanedStep {
		return ErrRecoveryBlocked
	}
	identity, err := s.LoadSiteLifetime(ctx, op.SiteID)
	if err != nil || identity.AllocatingJob != jid || s.CheckFreshSiteLifetime(ctx, identity) != nil {
		return ErrRecoveryBlocked
	}
	w := op.Identity.WordPress
	if identity.Name != w.Input.SiteName || identity.UID != w.Input.UID || identity.GID != w.Input.GID {
		return ErrRecoveryBlocked
	}
	q.mu.Lock()
	root := q.configCustody
	q.mu.Unlock()
	if root == "" {
		return ErrRecoveryBlocked
	}
	b := configcustody.ConfigBinding{JobID: jid, SiteID: op.SiteID, DatabaseID: op.DatabaseID, AccountID: op.Identity.Database.PrimaryUserID, Docroot: identity.Docroot, UID: identity.UID, GID: identity.GID}
	return configcustody.RemoveConfigRecord(ctx, root, b, seal)
}

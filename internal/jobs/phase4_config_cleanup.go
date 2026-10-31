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
	w := op.Identity.WordPress
	plan, err := wordpress.BuildInstall(w.Input, w.Evidence)
	if err != nil {
		return errors.New("jobs: config terminal reconciliation refused")
	}
	b := configcustody.ConfigBinding{JobID: jid, SiteID: op.SiteID, DatabaseID: op.DatabaseID, AccountID: op.Identity.Database.PrimaryUserID, Docroot: plan.Docroot, UID: w.Input.UID, GID: w.Input.GID}
	return configcustody.RemoveConfigRecord(ctx, root, b, seal)
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
		release := q.locks.lockSet(op.Keys())
		err = q.reconcileConfigRecord(ctx, jid)
		release()
		if err != nil {
			return err
		}
	}
	return nil
}

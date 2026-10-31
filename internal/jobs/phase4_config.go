package jobs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync/atomic"

	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

// GenerateWordPressCredentials runs at trusted admission, before sealing/effects.
// Every password and salt has independent local entropy. No remote salt service.
func GenerateWordPressCredentials() (WordPressCredentials, error) {
	var c WordPressCredentials
	fields := []*string{&c.DatabasePassword, &c.AdminPassword}
	for i := range c.Salts {
		fields = append(fields, &c.Salts[i])
	}
	for _, field := range fields {
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return WordPressCredentials{}, errors.New("jobs: WordPress credential generation failed")
		}
		*field = base64.RawURLEncoding.EncodeToString(raw[:])
		clear(raw[:])
	}
	return c, nil
}

type configWorkerKey struct{}
type configWorkerLease struct {
	queue  *Queue
	job    string
	active atomic.Bool
}

// ConfigIsolation is a trusted runtime dependency, never an RPC argument. It must
// resolve and match the reserved non-login UID/GID, quiesce all site-UID writers,
// exclude trusted root relocation and unverified docroot entries, then invoke the
// effect synchronously once while retaining that protection. Failure must keep
// the site non-serving. A mere operation lock or chmod is not isolation.
// There is deliberately no default implementation or production activation.
type ConfigIsolation func(context.Context, configcustody.ConfigBinding, func(context.Context) error) error

// CreateWordPressConfig composes operation-owned data and publishes it through
// sealed no-replace custody. Only the queue's original worker, holding the full
// persisted operation lock set, may call it. It does not install/verify WordPress
// or mark the operation terminal, nor attest the current docroot pathname.
func (q *Queue) CreateWordPressConfig(ctx context.Context, jobID, custody string, isolate ConfigIsolation) error {
	refuse := errors.New("jobs: WordPress config creation refused")
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	if lease == nil || lease.queue != q || lease.job != jobID || !lease.active.Load() || isolate == nil {
		return refuse
	}
	s, ok := q.store.(interface {
		operationStore
		CheckpointOperation(context.Context, string, string, string) error
	})
	if !ok {
		return refuse
	}
	seal, ok := q.sealer.(*secret.Sealer)
	if !ok {
		return refuse
	}
	if err := q.healthy(); err != nil {
		return refuse
	}
	credentials, err := q.OpenWordPressCredentials(ctx, jobID)
	if err != nil {
		return refuse
	}
	op, err := s.LoadOperation(ctx, jobID)
	if err != nil || op.Kind != store.WordPressInstall || (op.Checkpoint != "db_applied" && op.Checkpoint != "config_published") {
		return refuse
	}
	w := op.Identity.WordPress
	plan, err := wordpress.BuildInstall(w.Input, w.Evidence)
	if err != nil {
		return refuse
	}
	binding := configcustody.ConfigBinding{JobID: jobID, SiteID: op.SiteID, DatabaseID: op.DatabaseID, AccountID: op.Identity.Database.PrimaryUserID, Docroot: plan.Docroot, UID: w.Input.UID, GID: w.Input.GID}
	raw, err := configcustody.ComposeConfig(op.Identity.Database.Name, op.Identity.Accounts[0].Username, credentials.DatabasePassword, credentials.Salts)
	if err != nil {
		return refuse
	}
	defer clear(raw)
	called := false
	succeeded := false
	err = isolate(ctx, binding, func(_ context.Context) error {
		if called || !lease.active.Load() {
			return refuse
		}
		called = true
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.CheckOperation(ctx, jobID); err != nil {
			return refuse
		}
		if err := configcustody.PublishConfig(ctx, custody, binding, raw, seal); err != nil {
			return err
		}
		if op.Checkpoint == "db_applied" {
			if err := s.CheckpointOperation(ctx, jobID, "db_applied", "config_published"); err != nil {
				return err
			}
		}
		succeeded = true
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return rpcerr.Errf("interrupted", "WordPress config recovery required")
	}
	if !called || !succeeded {
		return rpcerr.Errf("interrupted", "WordPress config recovery required")
	}
	return nil
}

package jobs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
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
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	runs   sync.WaitGroup
}

// Admission and revocation share a lock so no Add can race the drain's Wait.
func (l *configWorkerLease) admit(ctx context.Context) (context.Context, func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.active.Load() || l.ctx.Err() != nil || ctx.Err() != nil {
		return nil, nil, false
	}
	l.runs.Add(1)
	call, cancel := context.WithCancel(l.ctx)
	stop := context.AfterFunc(ctx, cancel)
	return call, func() { stop(); cancel(); l.runs.Done() }, true
}

func (l *configWorkerLease) close() {
	l.mu.Lock()
	l.active.Store(false)
	l.cancel()
	l.mu.Unlock()
	l.runs.Wait()
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
	return runConfigIsolation(ctx, binding, isolate, func(effectCtx context.Context) error {
		if !lease.active.Load() {
			return refuse
		}
		if err := effectCtx.Err(); err != nil {
			return err
		}
		if err := s.CheckOperation(effectCtx, jobID); err != nil {
			return refuse
		}
		if err := configcustody.PublishConfig(effectCtx, custody, binding, raw, seal); err != nil {
			return err
		}
		if op.Checkpoint == "db_applied" {
			if err := s.CheckpointOperation(effectCtx, jobID, "db_applied", "config_published"); err != nil {
				return err
			}
		}
		return nil
	})
}

// runConfigIsolation admits one effect and revokes admission as soon as isolation
// returns. A broken dependency cannot retain the effect's secrets or race buffer
// clearing. An already admitted asynchronous effect is canceled and drained before
// returning. This contains misuse, but cannot replace the dependency's obligation
// to retain real site isolation until the synchronous callback has completed.
func runConfigIsolation(ctx context.Context, binding configcustody.ConfigBinding, isolate ConfigIsolation, effect func(context.Context) error) error {
	refuse := errors.New("jobs: WordPress config isolation refused")
	effectCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	open, called, active, failed, succeeded := true, false, false, false, false
	done := make(chan struct{})
	callback := func(context.Context) (err error) {
		mu.Lock()
		if !open || called {
			failed = true
			mu.Unlock()
			return refuse
		}
		called, active = true, true
		mu.Unlock()
		defer func() {
			if recover() != nil {
				err = refuse
			}
			mu.Lock()
			active = false
			succeeded = err == nil
			failed = failed || err != nil
			close(done)
			mu.Unlock()
		}()
		return effect(effectCtx)
	}
	isolationErr := func() (err error) {
		defer func() {
			if recover() != nil {
				err = refuse
			}
		}()
		return isolate(ctx, binding, callback)
	}()
	mu.Lock()
	open = false
	if active {
		failed = true
	}
	wait := called
	mu.Unlock()
	cancel()
	if wait {
		<-done
	}
	mu.Lock()
	defer mu.Unlock()
	// A retained callback captures only this revoked admission state, not the
	// operation closure containing plaintext and store/descriptor dependencies.
	effect = nil
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !called {
		return refuse // No effect began. Do not preserve arbitrary terminal failures.
	}
	if isolationErr != nil || failed || !succeeded {
		return rpcerr.Errf("interrupted", "WordPress config recovery required")
	}
	return nil
}

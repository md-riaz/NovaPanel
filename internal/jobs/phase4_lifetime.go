package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	runner "novapanel/internal/exec"
	"novapanel/internal/php"
	"novapanel/internal/platform"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

type installLifetimeDomain interface {
	WithEmpty(context.Context, func(context.Context) error) error
	Run(context.Context, string, []string, runner.RunOpts) ([]byte, []byte, int, error)
	Close(context.Context) error
}

type lifetimeStore interface {
	operationStore
	CheckFreshSiteLifetime(context.Context, store.SiteLifetime) error
	ActivateSiteLifetime(context.Context, store.SiteLifetime) error
}

// WordPressLifetime remains held until the operation and its serving decision finish.
type WordPressLifetime struct {
	mu                                              sync.Mutex
	domain                                          installLifetimeDomain
	binding                                         configcustody.ConfigBinding
	identity                                        store.SiteLifetime
	activate                                        func(context.Context, store.SiteLifetime) error
	closed, failed, published, successful, released bool
	stage                                           int
	plan                                            wordpress.Plan
	check                                           func(context.Context) error
	parent                                          context.Context
	ctx                                             context.Context
	cancel                                          context.CancelFunc
}

// reviewedInstallChild is intentionally package-private until Slice 1b supplies
// the fixed reviewed WordPress commands; it is not a general launch capability.
type reviewedInstallChild struct {
	kind    wordpress.StepKind
	options runner.RunOpts
}

func (l *WordPressLifetime) runReviewed(ctx context.Context, child reviewedInstallChild) ([]byte, []byte, int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.failed || l.successful || child.options.Cgroup != nil || child.options.Uid != 0 || child.options.Gid != 0 || len(child.options.Groups) != 0 {
		return nil, nil, -1, errors.New("jobs: reviewed child refused")
	}
	work, cancel := context.WithCancel(l.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	ctx = work
	steps := []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums, wordpress.CreateConfig, wordpress.InstallCore}
	index := l.stage
	if index > 3 {
		index--
	}
	if l.stage == 3 || index >= len(steps) || child.kind != steps[index] {
		l.failed = true
		return nil, nil, -1, errors.New("jobs: reviewed child out of order")
	}
	var step *wordpress.Step
	for i := range l.plan.Steps {
		if l.plan.Steps[i].Kind == child.kind {
			step = &l.plan.Steps[i]
			break
		}
	}
	if step == nil || step.Program == "" || ((step.Stdin == "") != (child.options.Stdin == nil)) {
		l.failed = true
		return nil, nil, -1, errors.New("jobs: reviewed child unavailable")
	}
	if err := l.check(ctx); err != nil {
		l.failed = true
		return nil, nil, -1, err
	}
	child.options.Dir, child.options.Env = l.plan.Dir, l.plan.Env
	child.options.Uid, child.options.Gid, child.options.Groups = l.identity.UID, l.identity.GID, []uint32{}
	out, stderr, code, err := l.domain.Run(ctx, step.Program, step.Args, child.options)
	if err != nil || code != 0 {
		l.failed = true
	} else {
		l.stage++
	}
	return out, stderr, code, err
}

// ConfigIsolation executes the effect synchronously inside the held empty window.
func (l *WordPressLifetime) ConfigIsolation(ctx context.Context, binding configcustody.ConfigBinding, effect func(context.Context) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.failed || l.successful || l.published || l.stage != 3 || binding != l.binding || effect == nil {
		l.failed = true
		return errors.New("jobs: lifetime config isolation refused")
	}
	work, cancel := context.WithCancel(l.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	ctx = work
	if err := l.check(ctx); err != nil {
		l.failed = true
		return err
	}
	l.published = true
	err := l.domain.WithEmpty(ctx, effect)
	if err != nil {
		l.failed = true
	} else {
		l.stage++
	}
	return err
}

// ReleaseServing is the Slice 1b serving-enable seam. The caller may invoke it
// only after the operation has returned successfully and teardown has succeeded.
func (l *WordPressLifetime) ReleaseServing(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed || !l.successful || l.failed || l.released {
		return errors.New("jobs: serving release refused")
	}
	if err := l.parent.Err(); err != nil {
		l.failed = true
		return err
	}
	if err := l.check(ctx); err != nil {
		l.failed = true
		return err
	}
	if err := l.activate(ctx, l.identity); err != nil {
		l.failed = true
		return err
	}
	l.released = true
	return nil
}

// withInstallLifetime revokes retained callers and drains before enabling serving.
func withInstallLifetime(ctx context.Context, l *WordPressLifetime, operation func(*WordPressLifetime) error, release func(*WordPressLifetime) error) (err error) {
	l.parent = ctx
	l.ctx, l.cancel = context.WithCancel(ctx)
	defer func() {
		l.cancel()
		if recover() != nil {
			err = errors.Join(err, errors.New("jobs: lifetime operation panicked"))
		}
		l.mu.Lock()
		l.closed = true
		failed := l.failed
		published := l.published && l.stage == 5
		l.mu.Unlock()
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		closeErr := l.domain.Close(cleanup)
		err = errors.Join(err, closeErr)
		if err != nil || failed || !published || ctx.Err() != nil {
			if err == nil {
				err = errors.New("jobs: lifetime operation incomplete")
			}
			return
		}
		l.mu.Lock()
		l.successful = true
		l.mu.Unlock()
		err = func() (releaseErr error) {
			defer func() {
				if recover() != nil {
					releaseErr = errors.New("jobs: serving release panicked")
				}
			}()
			return release(l)
		}()
		l.mu.Lock()
		released := l.released
		l.successful = false
		l.mu.Unlock()
		if err == nil && !released {
			err = errors.New("jobs: serving release omitted")
		}
	}()
	if operation == nil || release == nil {
		return errors.New("jobs: lifetime consumer unavailable")
	}
	return operation(l)
}

// WithFreshWordPressLifetime holds the existing operation locks and install
// domain for the complete callback. Slice 1b owns the callback and FPM enable.
func (q *Queue) WithFreshWordPressLifetime(ctx context.Context, jid string, identity store.SiteLifetime, operation func(*WordPressLifetime) error, release func(*WordPressLifetime) error) error {
	lease, _ := ctx.Value(configWorkerKey{}).(*configWorkerLease)
	if lease == nil || lease.queue != q || lease.job != jid || !lease.active.Load() || q.healthy() != nil {
		return errors.New("jobs: lifetime worker refused")
	}
	ctx, done, ok := lease.admit(ctx)
	if !ok {
		return errors.New("jobs: lifetime worker refused")
	}
	defer done()
	s, ok := q.store.(lifetimeStore)
	if !ok {
		return errors.New("jobs: lifetime persistence unavailable")
	}
	op, err := s.LoadOperation(ctx, jid)
	if err != nil {
		return err
	}
	if op.Kind != store.WordPressInstall || op.Identity.WordPress == nil || op.SiteID != identity.SiteID {
		return errors.New("jobs: lifetime operation mismatch")
	}
	held, _ := ctx.Value(heldLocksKey{}).(map[string]bool)
	for _, key := range op.Keys() {
		if !held[key] {
			return errors.New("jobs: lifetime operation locks missing")
		}
	}
	in := op.Identity.WordPress.Input
	if identity.Name != in.SiteName || identity.UID != in.UID || identity.GID != in.GID || identity.Docroot != filepath.Join(in.SitesRoot, in.SiteName, "public") {
		return errors.New("jobs: lifetime identity mismatch")
	}
	unlock, err := q.LockSiteOSUser(ctx, identity.SiteID, identity.Name)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err = q.OpenWordPressCredentials(ctx, jid); err != nil {
		return err
	}
	if err = s.CheckFreshSiteLifetime(ctx, identity); err != nil {
		if errors.Is(err, store.ErrSitePrecondition) {
			return rpcerr.Errf("precondition_failed", "site account is not fresh")
		}
		return err
	}
	if err = php.VerifyUser(ctx, runner.Real{}, in.SitesRoot, identity.Name, identity.SiteID); err != nil {
		return err
	}
	if _, err = platform.NewSiteRunner(ctx, runner.Real{}, in.SitesRoot, identity.Name, identity.UID, identity.GID); err != nil {
		return err
	}
	// A fresh account has no managed launchers until its explicit serving release;
	// the general launcher gate and IO retirement belong to Slice 2.
	plan, err := wordpress.BuildInstall(in, op.Identity.WordPress.Evidence)
	if err != nil {
		return err
	}
	domain, err := platform.CreateInstallDomain(identity.SiteID)
	if err != nil {
		return err
	}
	binding := configcustody.ConfigBinding{JobID: jid, SiteID: identity.SiteID, DatabaseID: op.DatabaseID, AccountID: op.Identity.Database.PrimaryUserID, Docroot: identity.Docroot, UID: identity.UID, GID: identity.GID}
	l := &WordPressLifetime{domain: domain, binding: binding, identity: identity, plan: plan, activate: s.ActivateSiteLifetime, check: func(checkCtx context.Context) error {
		if err := q.healthy(); err != nil {
			return err
		}
		return s.CheckOperation(checkCtx, jid)
	}}
	return withInstallLifetime(ctx, l, operation, release)
}

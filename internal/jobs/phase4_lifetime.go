package jobs

import (
	"context"
	"errors"
	"os"
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
	SiteByID(context.Context, string) (store.Site, error)
	CheckFreshSiteLifetime(context.Context, store.SiteLifetime) error
	CheckActivatedSiteLifetime(context.Context, store.SiteLifetime, string) error
	ActivateSiteLifetime(context.Context, store.SiteLifetime) error
	CheckpointOperation(context.Context, string, string, string) error
}

// WordPressLifetime remains held until the operation and its serving decision finish.
type WordPressLifetime struct {
	mu                                              sync.Mutex
	domain                                          installLifetimeDomain
	binding                                         configcustody.ConfigBinding
	identity                                        store.SiteLifetime
	activate                                        func(context.Context, store.SiteLifetime) error
	closed, failed, published, successful, released bool
	finalizeOnly                                    bool
	stage                                           int
	verified                                        func() error
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
	if l.finalizeOnly || l.closed || l.failed || l.successful || child.options.Cgroup != nil || child.options.Uid != 0 || child.options.Gid != 0 || len(child.options.Groups) != 0 {
		return nil, nil, -1, errors.New("jobs: reviewed child refused")
	}
	work, cancel := context.WithCancel(l.ctx)
	stop := context.AfterFunc(ctx, cancel)
	defer func() { stop(); cancel() }()
	ctx = work
	steps := []wordpress.StepKind{wordpress.Download, wordpress.VerifyChecksums, wordpress.IsInstalled, wordpress.InstallCore}
	index := l.stage
	if index > 2 {
		index--
	}
	if l.stage == 2 || index >= len(steps) || child.kind != steps[index] {
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
	if child.kind == wordpress.IsInstalled && code == 1 && runner.NormalExit(err, code) {
		err = nil
	}
	if err != nil || (code != 0 && !(child.kind == wordpress.IsInstalled && code == 1)) {
		l.failed = true
	} else if child.kind == wordpress.IsInstalled && code == 0 {
		l.stage += 2
	} else {
		l.stage++
	}
	return out, stderr, code, err
}

// ConfigIsolation executes the effect synchronously inside the held empty window.
// The effect must not re-enter this lifetime: it runs under its mutex.
func (l *WordPressLifetime) ConfigIsolation(ctx context.Context, binding configcustody.ConfigBinding, effect func(context.Context) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finalizeOnly || l.closed || l.failed || l.successful || l.published || l.stage != 2 || binding != l.binding || effect == nil {
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

// activateServing commits the disposition before the owner calls serving enable.
func (l *WordPressLifetime) activateServing(ctx context.Context) error {
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
		if err = l.activateServing(ctx); err != nil {
			return
		}
		err = callServingRelease(l, release)
		if err != nil {
			err = errors.Join(ErrRetry, err)
		}
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

func callServingRelease(l *WordPressLifetime, release func(*WordPressLifetime) error) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("jobs: serving release panicked")
		}
	}()
	return release(l)
}

func replayActivatedLifetime(ctx context.Context, domain installLifetimeDomain, identity store.SiteLifetime, check func(context.Context) error, release func(*WordPressLifetime) error) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	err := domain.Close(cleanup)
	cancel()
	if err != nil {
		return err
	}
	if release == nil {
		return errors.New("jobs: lifetime consumer unavailable")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = check(ctx); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = callServingRelease(&WordPressLifetime{identity: identity, closed: true, released: true, finalizeOnly: true}, release); err != nil {
		return errors.Join(ErrRetry, err)
	}
	return nil
}

// WithFreshWordPressLifetime holds the existing operation locks and install
// domain for the complete callback. The serving callback must ensure its state
// idempotently, because activation may have committed before a crash.
func (q *Queue) WithFreshWordPressLifetime(ctx context.Context, jid string, identity store.SiteLifetime, operation func(*WordPressLifetime) error, release func(*WordPressLifetime) error) error {
	return q.withFreshWordPressLifetime(ctx, jid, identity, operation, release, func(ctx context.Context, root string, identity store.SiteLifetime) error {
		if err := verifyWordPressTool(ctx); err != nil {
			return err
		}
		if err := php.VerifyUser(ctx, runner.Real{}, root, identity.Name, identity.SiteID); err != nil {
			return err
		}
		if err := wordpress.EnsureToolWorkspace(root, identity.Name, identity.UID, identity.GID); err != nil {
			return err
		}
		_, err := platform.NewSiteRunner(ctx, runner.Real{}, root, identity.Name, identity.UID, identity.GID)
		return err
	}, func(siteID string) (installLifetimeDomain, error) { return platform.CreateInstallDomain(siteID) })
}

func (q *Queue) withFreshWordPressLifetime(ctx context.Context, jid string, identity store.SiteLifetime, operation func(*WordPressLifetime) error, release func(*WordPressLifetime) error, verify func(context.Context, string, store.SiteLifetime) error, createDomain func(string) (installLifetimeDomain, error)) error {
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
	site, err := s.SiteByID(ctx, identity.SiteID)
	if err != nil {
		return err
	}
	if site.Status != "creating" {
		return rpcerr.Errf("precondition_failed", "site account is not non-serving")
	}
	if identity.AllocatingJob != jid {
		return rpcerr.Errf("precondition_failed", "site account belongs to another operation")
	}
	replay := false
	if err = s.CheckFreshSiteLifetime(ctx, identity); err != nil {
		if !errors.Is(err, store.ErrSitePrecondition) {
			return err
		}
		if err = s.CheckActivatedSiteLifetime(ctx, identity, jid); err != nil {
			if errors.Is(err, store.ErrSitePrecondition) {
				return rpcerr.Errf("precondition_failed", "site account is not fresh")
			}
			return err
		}
		replay = true
	}
	if operation == nil || release == nil {
		return errors.New("jobs: lifetime consumer unavailable")
	}
	if _, err = q.OpenWordPressCredentials(ctx, jid); err != nil {
		return err
	}
	if err = verify(ctx, in.SitesRoot, identity); err != nil {
		return err
	}
	// A fresh account has no managed launchers until its explicit serving release;
	// the general launcher gate and IO retirement belong to Slice 2.
	plan, err := wordpress.BuildInstall(in, op.Identity.WordPress.Evidence)
	if err != nil {
		return err
	}
	program, err := os.Executable()
	if err != nil {
		return err
	}
	for i := range plan.Steps {
		if plan.Steps[i].Kind == wordpress.Download {
			plan.Steps[i].Program = program
			plan.Steps[i].Args = []string{"__wordpress-extract", identity.Docroot}
		}
	}
	domain, err := createDomain(identity.SiteID)
	if err != nil {
		return err
	}
	if replay {
		// docs/phase4-production-activation-ruling.md: activated replay is finalize-only.
		return replayActivatedLifetime(ctx, domain, identity, func(ctx context.Context) error {
			if err := q.healthy(); err != nil {
				return err
			}
			if err := s.CheckOperation(ctx, jid); err != nil {
				return err
			}
			return s.CheckActivatedSiteLifetime(ctx, identity, jid)
		}, release)
	}
	binding := configcustody.ConfigBinding{JobID: jid, SiteID: identity.SiteID, DatabaseID: op.DatabaseID, AccountID: op.Identity.Database.PrimaryUserID, Docroot: identity.Docroot, UID: identity.UID, GID: identity.GID}
	l := &WordPressLifetime{domain: domain, binding: binding, identity: identity, plan: plan, activate: s.ActivateSiteLifetime, check: func(checkCtx context.Context) error {
		if err := q.healthy(); err != nil {
			return err
		}
		if err := s.CheckOperation(checkCtx, jid); err != nil {
			return err
		}
		current, err := s.LoadOperation(checkCtx, jid)
		if err != nil {
			return err
		}
		checkpoint := current.Checkpoint
		resumable := checkpoint == "verified_resumable" || checkpoint == "config_published" || checkpoint == "verified"
		if resumable {
			if err := s.CheckpointOperation(checkCtx, jid, checkpoint, "verification_in_progress"); err != nil {
				return err
			}
		}
		if err := verify(checkCtx, in.SitesRoot, identity); err != nil {
			return err
		}
		if resumable {
			return s.CheckpointOperation(checkCtx, jid, "verification_in_progress", checkpoint)
		}
		return nil
	}}
	return withInstallLifetime(ctx, l, operation, release)
}

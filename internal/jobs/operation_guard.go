package jobs

import (
	"context"
	"errors"
	"golang.org/x/sync/semaphore"
	"sync"
)

type mutationKey struct{}
type mutationLease struct {
	mu        sync.Mutex
	queue     *Queue
	active    bool
	refs      int
	drained   chan struct{}
	exclusive bool
	ctx       context.Context
	cancel    context.CancelFunc
}

func (q *Queue) rejectRecoveryReentry(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if lease, ok := ctx.Value(mutationKey{}).(*mutationLease); ok && lease.queue == q {
		return ErrRecoveryBlocked
	}
	return nil
}

// AdmitMutation holds recovery exclusion for the full mutation lifetime. The
// derived context permits synchronous nested admission without a second read
// lock (which would deadlock behind a waiting recovery writer).
func (q *Queue) AdmitMutation(ctx context.Context) (context.Context, func(), error) {
	if err := ctx.Err(); err != nil {
		return ctx, nil, err
	}
	if lease, ok := ctx.Value(mutationKey{}).(*mutationLease); ok && lease.queue == q {
		lease.mu.Lock()
		if lease.active && lease.ctx.Err() == nil {
			lease.refs++
			lease.mu.Unlock()
			child, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(lease.ctx, cancel)
			return child, func() { stop(); cancel(); lease.release(false) }, nil
		}
		lease.mu.Unlock()
		return ctx, nil, ErrRecoveryBlocked
	}
	if err := q.operationMu.RLockContext(ctx); err != nil {
		return ctx, nil, err
	}
	if err := ctx.Err(); err != nil {
		q.operationMu.RUnlock()
		return ctx, nil, err
	}
	if q.recoveryBlocked || q.startupBlocked {
		q.operationMu.RUnlock()
		return ctx, nil, errors.Join(ErrRecoveryBlocked, errors.New("recovery incomplete"))
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	lease := &mutationLease{queue: q, active: true, refs: 1, drained: make(chan struct{}), ctx: leaseCtx, cancel: cancel}
	return context.WithValue(leaseCtx, mutationKey{}, lease), func() { lease.release(true) }, nil
}

func (l *mutationLease) release(root bool) {
	l.mu.Lock()
	if root {
		l.active = false
		l.cancel()
	}
	l.refs--
	last := l.refs == 0
	l.mu.Unlock()
	if last {
		close(l.drained)
	}
	if root {
		<-l.drained
		if !l.exclusive {
			l.queue.operationMu.RUnlock()
		}
	}
}

type operationGuard struct {
	once sync.Once
	sem  *semaphore.Weighted
}

const operationGuardCapacity int64 = 1 << 30

func (g *operationGuard) init() {
	g.once.Do(func() { g.sem = semaphore.NewWeighted(operationGuardCapacity) })
}
func (g *operationGuard) LockContext(ctx context.Context) error {
	g.init()
	return g.sem.Acquire(ctx, operationGuardCapacity)
}
func (g *operationGuard) RLockContext(ctx context.Context) error {
	g.init()
	return g.sem.Acquire(ctx, 1)
}
func (g *operationGuard) Lock() {
	if err := g.LockContext(context.Background()); err != nil {
		panic(err)
	}
}
func (g *operationGuard) Unlock() { g.sem.Release(operationGuardCapacity) }
func (g *operationGuard) RLock() {
	if err := g.RLockContext(context.Background()); err != nil {
		panic(err)
	}
}
func (g *operationGuard) RUnlock()       { g.sem.Release(1) }
func (g *operationGuard) TryRLock() bool { g.init(); return g.sem.TryAcquire(1) }

package jobs

import (
	"context"
	"golang.org/x/sync/semaphore"
	"sync"
)

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

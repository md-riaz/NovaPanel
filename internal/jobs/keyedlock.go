package jobs

import (
	"context"
	"novapanel/internal/store"
	"sync"
)

func (k *keyedLock) lockSet(keys []string) func() {
	keys = store.OrderedOperationKeys(keys)
	releases := make([]func(), 0, len(keys))
	for _, key := range keys {
		releases = append(releases, k.lock(key))
	}
	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
}

type lockRef struct {
	gate chan struct{}
	refs int
}
type keyedLock struct {
	mu   sync.Mutex
	keys map[string]*lockRef
}

func (k *keyedLock) lock(key string) func() {
	release, err := k.lockContext(context.Background(), key)
	if err != nil {
		panic(err)
	}
	return release
}
func (k *keyedLock) lockContext(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k.mu.Lock()
	if k.keys == nil {
		k.keys = make(map[string]*lockRef)
	}
	r := k.keys[key]
	if r == nil {
		r = &lockRef{gate: make(chan struct{}, 1)}
		k.keys[key] = r
	}
	r.refs++
	k.mu.Unlock()
	drop := func() {
		k.mu.Lock()
		r.refs--
		if r.refs == 0 {
			delete(k.keys, key)
		}
		k.mu.Unlock()
	}
	select {
	case r.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-r.gate
			drop()
			return nil, err
		}
		return func() { <-r.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

package jobs

import (
	"context"
	"sync"
)

type lockRef struct {
	token chan struct{}
	refs  int
}
type keyedLock struct {
	mu   sync.Mutex
	keys map[string]*lockRef
}

func (k *keyedLock) lock(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k.mu.Lock()
	if k.keys == nil {
		k.keys = make(map[string]*lockRef)
	}
	r := k.keys[key]
	if r == nil {
		r = &lockRef{token: make(chan struct{}, 1)}
		r.token <- struct{}{}
		k.keys[key] = r
	}
	r.refs++
	k.mu.Unlock()
	releaseRef := func() {
		k.mu.Lock()
		r.refs--
		if r.refs == 0 {
			delete(k.keys, key)
		}
		k.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	case <-r.token:
		if err := ctx.Err(); err != nil {
			r.token <- struct{}{}
			releaseRef()
			return nil, err
		}
		return func() { r.token <- struct{}{}; releaseRef() }, nil
	}
}

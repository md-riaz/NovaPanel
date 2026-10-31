package jobs

import (
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
	mu   sync.Mutex
	refs int
}
type keyedLock struct {
	mu   sync.Mutex
	keys map[string]*lockRef
}

func (k *keyedLock) lock(key string) func() {
	k.mu.Lock()
	if k.keys == nil {
		k.keys = make(map[string]*lockRef)
	}
	r := k.keys[key]
	if r == nil {
		r = &lockRef{}
		k.keys[key] = r
	}
	r.refs++
	k.mu.Unlock()
	r.mu.Lock()
	return func() {
		r.mu.Unlock()
		k.mu.Lock()
		r.refs--
		if r.refs == 0 {
			delete(k.keys, key)
		}
		k.mu.Unlock()
	}
}

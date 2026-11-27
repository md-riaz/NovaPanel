//go:build wordpress_offline_lab && linux

package store

import (
	"context"
	"sync"
)

var freshHook struct {
	sync.RWMutex
	fn func(context.Context, string, string)
}

func SetFreshCheckpointTestHook(fn func(context.Context, string, string)) {
	freshHook.Lock()
	defer freshHook.Unlock()
	freshHook.fn = fn
}
func freshCheckpointTestHook(ctx context.Context, jid, checkpoint string) {
	freshHook.RLock()
	fn := freshHook.fn
	freshHook.RUnlock()
	if fn != nil {
		fn(ctx, jid, checkpoint)
	}
}

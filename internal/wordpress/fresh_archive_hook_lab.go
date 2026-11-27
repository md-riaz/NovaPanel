//go:build wordpress_offline_lab && linux

package wordpress

import "sync"

var freshArchiveHook struct {
	sync.RWMutex
	fn func()
}

func SetFreshArchiveTestHook(fn func()) {
	freshArchiveHook.Lock()
	defer freshArchiveHook.Unlock()
	freshArchiveHook.fn = fn
}
func freshArchiveTestHook() {
	freshArchiveHook.RLock()
	fn := freshArchiveHook.fn
	freshArchiveHook.RUnlock()
	if fn != nil {
		fn()
	}
}

package auth

import "sync"

// A bounded cache fails closed when full; live failures are never evicted.
const maxFailureAccounts = 4096

type failureBudget struct {
	start int64
	count int
}
type FailureBudgets struct {
	mu       sync.Mutex
	accounts map[string]failureBudget
}

// Verify serializes admission with its outcome so concurrent failures cannot
// exceed the budget. Internal errors neither charge nor reset a credential budget.
func (b *FailureBudgets) Verify(key string, now int64, limit int, window int64, verify func() (bool, error)) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.accounts == nil {
		b.accounts = make(map[string]failureBudget)
	}
	for k, r := range b.accounts {
		// Queued attempts may carry older timestamps; only elapsed windows expire.
		if now >= r.start && now-r.start >= window {
			delete(b.accounts, k)
		}
	}
	r, exists := b.accounts[key]
	if r.count >= limit || (!exists && len(b.accounts) >= maxFailureAccounts) {
		return false, nil
	}
	ok, e := verify()
	if e != nil {
		return false, e
	}
	if ok {
		delete(b.accounts, key)
	} else {
		if !exists {
			r.start = now
		}
		r.count++
		b.accounts[key] = r
	}
	return ok, nil
}

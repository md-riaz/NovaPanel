package auth

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestFailureBudgetResetWindowAndIsolation(t *testing.T) {
	var b FailureBudgets
	attempts := 0
	verify := func(ok bool) func() (bool, error) { return func() (bool, error) { attempts++; return ok, nil } }
	for _, ok := range []bool{false, true, false, false} {
		got, e := b.Verify("password:alice", 1000, 2, 17000, verify(ok))
		if e != nil || got != ok {
			t.Fatal(got, e)
		}
	}
	if ok, _ := b.Verify("password:alice", 17999, 2, 17000, verify(true)); ok || attempts != 4 {
		t.Fatal("exhausted verification ran", attempts)
	}
	if ok, _ := b.Verify("password:bob", 17999, 2, 17000, verify(true)); !ok {
		t.Fatal("other account blocked")
	}
	if ok, _ := b.Verify("password:alice", 18000, 2, 17000, verify(true)); !ok {
		t.Fatal("boundary blocked")
	}
}
func TestFailureBudgetOlderOtherAccountDoesNotResetLiveFailure(t *testing.T) {
	var b FailureBudgets
	if ok, err := b.Verify("alice", 1001, 1, 900000, func() (bool, error) { return false, nil }); ok || err != nil {
		t.Fatal(ok, err)
	}
	// Bob sampled its timestamp before waiting for Alice's verification.
	if ok, err := b.Verify("bob", 1000, 1, 900000, func() (bool, error) { return true, nil }); !ok || err != nil {
		t.Fatal("other account blocked", ok, err)
	}
	called := false
	ok, err := b.Verify("alice", 1002, 1, 900000, func() (bool, error) {
		called = true
		return true, nil
	})
	if called || ok || err != nil {
		t.Fatal("older timestamp reset another account's exhausted live budget", called, ok, err)
	}
}

func TestFailureBudgetOlderSameAccountCannotResetExhaustion(t *testing.T) {
	var b FailureBudgets
	if ok, err := b.Verify("alice", 1001, 1, 900000, func() (bool, error) { return false, nil }); ok || err != nil {
		t.Fatal(ok, err)
	}
	// A queued attempt may carry an older timestamp than the stored failure.
	called := false
	ok, err := b.Verify("alice", 1000, 1, 900000, func() (bool, error) {
		called = true
		return false, nil
	})
	if called || ok || err != nil {
		t.Fatal("older timestamp bypassed the exhausted account budget", called, ok, err)
	}
}

func TestFailureBudgetConcurrentAdmission(t *testing.T) {
	var b FailureBudgets
	var attempts atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Verify("mfa:user", 1000, 5, 900000, func() (bool, error) { attempts.Add(1); return false, nil })
		}()
	}
	wg.Wait()
	if attempts.Load() != 5 {
		t.Fatal(attempts.Load())
	}
}
func TestFailureBudgetMemoryBoundAndExpiry(t *testing.T) {
	var b FailureBudgets
	for i := 0; i < maxFailureAccounts; i++ {
		b.Verify(string(rune(i)), 1000, 1, 100, func() (bool, error) { return false, nil })
	}
	called := false
	b.Verify("overflow", 1000, 1, 100, func() (bool, error) { called = true; return false, nil })
	if called {
		t.Fatal("unbounded map")
	}
	b.Verify("overflow", 1100, 1, 100, func() (bool, error) { called = true; return false, nil })
	if !called {
		t.Fatal("expired entries not pruned")
	}
}

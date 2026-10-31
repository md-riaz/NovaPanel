package firewall

import (
	"context"
	"errors"
	"fmt"
	execpkg "novapanel/internal/exec"
	"sync"
	"testing"
	"time"
)

func reviewManager(f *managerFixture) runtimeManager {
	return runtimeManager{runner: f, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
}

func TestIndependentSuccessExitStatus(t *testing.T) {
	_, r := journalFixture(t)
	f := newManagerFixture(t, r)
	f.exists = true
	f.service["SuccessExitStatus"] = variant("(aiai)", []any{[]int{1}, []int{}})
	if e := reviewManager(f).ensure(context.Background(), r); e == nil {
		t.Fatal("ADMITTED SuccessExitStatus=1: helper refusal/failure may be success, disabling on-failure retry")
	}
}
func TestIndependentExtraTimerTriggers(t *testing.T) {
	for _, key := range []string{"OnClockChange", "OnTimezoneChange"} {
		t.Run(key, func(t *testing.T) {
			_, r := journalFixture(t)
			f := newManagerFixture(t, r)
			f.exists = true
			f.timer[key] = variant("b", true)
			if e := reviewManager(f).ensure(context.Background(), r); e == nil {
				t.Fatal("ADMITTED extra timer trigger", key)
			}
		})
	}
}
func TestIndependentExistingTimerAfterElapsedTime(t *testing.T) {
	_, r := journalFixture(t)
	f := newManagerFixture(t, r)
	f.exists = true
	m := reviewManager(f)
	m.now = func() time.Time { return time.Unix(95, 0) }
	m.monotonic = func() (uint64, error) { return 6000000, nil }
	// Original OnActive=10s started at mono=1s; next=11s matches wall deadline=100 exactly.
	if e := m.ensure(context.Background(), r); e != nil {
		t.Fatal("correct existing timer rejected after five seconds elapsed:", e)
	}
}
func TestIndependentExpiredExistingTimer(t *testing.T) {
	_, r := journalFixture(t)
	f := newManagerFixture(t, r)
	f.exists = true
	m := reviewManager(f)
	m.now = func() time.Time { return time.Unix(100, 0) }
	e := m.ensure(context.Background(), r)
	if e != nil {
		t.Fatal("immediate adoption failed", e)
	}
	for _, c := range f.calls {
		if c[0] == "/usr/bin/systemctl" && len(c) > 2 && c[2] == "start" {
			return
		}
	}
	t.Fatalf("expired authority with existing timer did not start service immediately: %v, calls=%v", e, f.calls)
}

type reviewChangingStats struct {
	base *managerFixture
	n    uint64
}

func (f *reviewChangingStats) Run(ctx context.Context, p string, a []string, o execpkg.RunOpts) ([]byte, []byte, int, error) {
	if p == "/usr/bin/busctl" && a[6] == "GetAll" && a[len(a)-1] == "org.freedesktop.systemd1.Service" {
		f.n++
		f.base.service["CPUUsageNSec"] = variant("t", f.n)
	}
	return f.base.Run(ctx, p, a, o)
}
func TestIndependentUnrelatedStatistics(t *testing.T) {
	_, r := journalFixture(t)
	f := newManagerFixture(t, r)
	f.exists = true
	m := reviewManager(f)
	m.runner = &reviewChangingStats{base: f}
	if e := m.ensure(context.Background(), r); e != nil {
		t.Fatal("safe unchanged contract refused for changing unrelated CPUUsageNSec:", e)
	}
}

type reviewConcurrentManager struct {
	mu       sync.Mutex
	base     *managerFixture
	arrived  int
	ready    chan struct{}
	launches int
}

func (f *reviewConcurrentManager) Run(ctx context.Context, p string, a []string, o execpkg.RunOpts) ([]byte, []byte, int, error) {
	f.mu.Lock()
	// Both independent startup callers finish absent-timer discovery before either launches.
	if p == "/usr/bin/busctl" && a[6] == "ListUnitsByNames" && a[len(a)-1] == f.base.r.Unit+".timer" && !f.base.exists {
		f.arrived++
		if f.arrived == 2 {
			close(f.ready)
		}
		f.mu.Unlock()
		select {
		case <-f.ready:
		case <-ctx.Done():
			return nil, nil, 1, ctx.Err()
		}
		return managerResponse("a(ssssssouso)", []any{[]any{}}), nil, 0, nil
	}
	defer f.mu.Unlock()
	if p == "/usr/bin/busctl" {
		servicePath := "/org/freedesktop/systemd1/unit/reviewservice"
		timerPath := "/org/freedesktop/systemd1/unit/reviewtimer"
		if a[6] == "GetUnit" {
			path := servicePath
			if a[len(a)-1] == f.base.r.Unit+".timer" {
				path = timerPath
			}
			return managerResponse("o", []any{path}), nil, 0, nil
		}
		if a[6] == "ListUnitsByNames" && f.base.exists {
			n := a[len(a)-1]
			path := servicePath
			if n == f.base.r.Unit+".timer" {
				path = timerPath
			}
			return managerResponse("a(ssssssouso)", []any{[]any{[]any{n, "", "loaded", "inactive", "dead", "", path, 0, "", "/"}}}), nil, 0, nil
		}
		if a[6] == "GetAll" && a[len(a)-1] == "org.freedesktop.systemd1.Unit" {
			props := map[string]any{}
			for k, v := range f.base.unit {
				props[k] = v
			}
			n := f.base.r.Unit + ".service"
			active, sub := "inactive", "dead"
			if a[4] == timerPath {
				n = f.base.r.Unit + ".timer"
				active, sub = "active", "waiting"
			}
			props["Id"] = variant("s", n)
			props["Names"] = variant("as", []string{n})
			props["ActiveState"] = variant("s", active)
			props["SubState"] = variant("s", sub)
			return managerResponse("a{sv}", []any{props}), nil, 0, nil
		}
	}
	if p == "/usr/bin/systemd-run" {
		f.launches++
		if f.base.exists {
			return nil, nil, 1, fmt.Errorf("unit already exists")
		}
	}
	return f.base.Run(ctx, p, a, o)
}
func TestIndependentConcurrentStartupAdoption(t *testing.T) {
	l, r, _ := runtimeFixture(t, true)
	f := &reviewConcurrentManager{base: newManagerFixture(t, r), ready: make(chan struct{})}
	m := reviewManager(f.base)
	m.runner = f
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- startupFirewall(context.Background(), l, m) }()
	}
	for i := 0; i < 2; i++ {
		e := <-done
		if !errors.Is(e, ErrFirewallRecoveryPending) || errors.Is(e, errManager) {
			t.Fatalf("startup adoption: %v", e)
		}
	}
	j, e := OpenJournal(l.root + "/journal")
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	after, p, e := j.Inspect(context.Background())
	if e != nil || !p || after != r {
		t.Fatal("journal changed", after, e)
	}
	t.Logf("both concurrent startup calls adopted exact configuration; launch attempts=%d", f.launches)
}

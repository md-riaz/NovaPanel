package firewall

import (
	"context"
	"errors"
	execpkg "novapanel/internal/exec"
	"testing"
	"time"
)

type interceptManager struct {
	base *managerFixture
	hook func(context.Context, string, []string) ([]byte, []byte, int, error, bool)
}

func (f interceptManager) Run(ctx context.Context, p string, a []string, o execpkg.RunOpts) ([]byte, []byte, int, error) {
	if f.hook != nil {
		b, s, c, e, handled := f.hook(ctx, p, a)
		if handled {
			return b, s, c, e
		}
	}
	return f.base.Run(ctx, p, a, o)
}
func TestRuntimeManagerImmediateAndExistingService(t *testing.T) {
	for _, state := range []string{"expired", "reverting", "existing-service"} {
		t.Run(state, func(t *testing.T) {
			_, r := journalFixture(t)
			f := newManagerFixture(t, r)
			now := int64(100)
			if state == "reverting" {
				now = 90
				r.State = "reverting"
			}
			if state == "existing-service" {
				now = 90
				f.exists = true
			}
			runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
				if p == "/usr/bin/busctl" && a[6] == "ListUnitsByNames" && a[len(a)-1] == r.Unit+".timer" {
					return managerResponse("a(ssssssouso)", []any{[]any{}}), nil, 0, nil, true
				}
				if p == "/usr/bin/systemd-run" || p == "/usr/bin/systemctl" {
					f.exists = true
					f.service["ControlGroup"] = variant("s", "/system.slice/"+r.Unit+".service")
					return nil, nil, 0, nil, true
				}
				if p == "/usr/bin/busctl" && a[6] == "GetAll" && a[len(a)-1] == "org.freedesktop.systemd1.Unit" && f.exists {
					props := map[string]any{}
					for k, v := range f.unit {
						props[k] = v
					}
					props["Id"] = variant("s", r.Unit+".service")
					props["Names"] = variant("as", []string{r.Unit + ".service"})
					props["ActiveState"] = variant("s", "active")
					props["SubState"] = variant("s", "running")
					f.service["ControlGroup"] = variant("s", "/system.slice/"+r.Unit+".service")
					return managerResponse("a{sv}", []any{props}), nil, 0, nil, true
				}
				return nil, nil, 0, nil, false
			}}
			m := runtimeManager{runner: runner, now: func() time.Time { return time.Unix(now, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
			if e := m.ensure(context.Background(), r); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRuntimeManagerAmbiguousLaunchAdoption(t *testing.T) {
	_, r := journalFixture(t)
	f := newManagerFixture(t, r)
	runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
		if p == "/usr/bin/systemd-run" {
			f.exists = true
			return nil, nil, 1, errors.New("ambiguous launch"), true
		}
		return nil, nil, 0, nil, false
	}}
	m := runtimeManager{runner: runner, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
	if e := m.ensure(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	if e := m.ensure(context.Background(), r); e != nil {
		t.Fatal("independent retry", e)
	}
}
func TestRuntimeManagerTransportNeverAbsence(t *testing.T) {
	_, r := journalFixture(t)
	for _, kind := range []string{"stderr", "oversized", "truncated", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			f := newManagerFixture(t, r)
			runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
				switch kind {
				case "stderr":
					return nil, []byte("No such unit"), 1, errors.New("transport"), true
				case "oversized":
					return make([]byte, managerLimit+1), nil, 0, nil, true
				case "truncated":
					return []byte(`{"type":`), nil, 0, nil, true
				default:
					return nil, nil, 1, context.Canceled, true
				}
			}}
			m := runtimeManager{runner: runner, now: time.Now, monotonic: managerMonotonic}
			if e := m.ensure(context.Background(), r); e == nil {
				t.Fatal("transport admitted")
			}
			if len(f.calls) != 0 {
				t.Fatal("mutated units")
			}
		})
	}
}
func TestRuntimeManagerTimerStopAfterConfirmation(t *testing.T) {
	j, r := journalFixture(t)
	mustCustody(t, j.Arm(context.Background(), r))
	f := newManagerFixture(t, r)
	stopped := false
	runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
		if p == "/usr/bin/systemctl" {
			if len(a) != 3 || a[0] != "--system" || a[1] != "stop" || a[2] != r.Unit+".timer" {
				t.Fatal(a)
			}
			got, e := j.read()
			if e != nil || got.State != "confirmed" {
				t.Fatal("stop before durable confirmation", got, e)
			}
			stopped = true
			return nil, nil, 0, nil, true
		}
		return nil, nil, 0, nil, false
	}}
	m := runtimeManager{runner: runner}
	mustCustody(t, j.Confirm(context.Background(), r.ID, func() time.Time { return time.Unix(90, 0) }, func(stem string) error { return m.stop(context.Background(), stem) }))
	if !stopped {
		t.Fatal("timer not stopped")
	}
}

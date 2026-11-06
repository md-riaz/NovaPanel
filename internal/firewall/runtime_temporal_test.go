package firewall

import (
	"context"
	"errors"
	"fmt"
	execpkg "novapanel/internal/exec"
	"os"
	"strings"
	"testing"
	"time"
)

func runTemporalChild(t *testing.T, root, action string) error {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, stderr, code, e := (execpkg.Recovery{}).Run(ctx, exe, []string{"-test.run=^TestRuntimeTemporalChild$"}, execpkg.RunOpts{Env: []string{"NP_TEMPORAL_ROOT=" + root, "NP_TEMPORAL_ACTION=" + action}})
	if e != nil || code != 0 {
		return fmt.Errorf("temporal child %d %v %s %s", code, e, out, stderr)
	}
	return nil
}
func TestRuntimeTemporalChild(t *testing.T) {
	root := os.Getenv("NP_TEMPORAL_ROOT")
	if root == "" {
		return
	}
	if os.Getenv("NP_TEMPORAL_ACTION") == "helper" {
		mustCustody(t, recoverInstalled(t.Context(), custodyID, runtimeLayout{root: root, rules: root + "/rules"}, func() time.Time { return time.Unix(100, 0) }, &restoreRunner{}))
		return
	}
	j, e := OpenJournal(root + "/journal")
	mustCustody(t, e)
	defer j.Close()
	mustCustody(t, j.Confirm(t.Context(), custodyID, func() time.Time { return time.Unix(90, 0) }, func(string) error { return nil }))
}

func TestRuntimeExistingTimerImmediateMatrix(t *testing.T) {
	for _, state := range []string{"armed", "confirming", "reverting"} {
		for _, waiting := range []bool{false, true} {
			t.Run(state+map[bool]string{false: "-spent", true: "-waiting"}[waiting], func(t *testing.T) {
				_, r := journalFixture(t)
				r.State = state
				f := newManagerFixture(t, r)
				f.exists = true
				runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
					if !waiting && p == "/usr/bin/busctl" && a[6] == "GetAll" && strings.HasSuffix(a[len(a)-1], ".Unit") {
						// Produce a spent timer without modifying the service state.
						n := ""
						for i := len(f.calls) - 1; i >= 0; i-- {
							c := f.calls[i]
							if len(c) > 7 && c[7] == "GetUnit" {
								n = c[len(c)-1]
								break
							}
						}
						if strings.HasSuffix(n, ".timer") {
							props := map[string]any{}
							for k, v := range f.unit {
								props[k] = v
							}
							props["Id"] = variant("s", n)
							props["Names"] = variant("as", []string{n})
							props["ActiveState"] = variant("s", "inactive")
							props["SubState"] = variant("s", "dead")
							return managerResponse("a{sv}", []any{props}), nil, 0, nil, true
						}
					}
					return nil, nil, 0, nil, false
				}}
				m := reviewManager(f)
				m.runner = runner
				if state != "reverting" {
					m.now = func() time.Time { return time.Unix(100, 0) }
				}
				if e := m.ensure(context.Background(), r); e != nil {
					t.Fatal(e)
				}
				starts := 0
				for _, c := range f.calls {
					if c[0] == "/usr/bin/systemctl" {
						starts++
						if strings.Join(c[1:], " ") != "--system start --no-block "+r.Unit+".service" {
							t.Fatal(c)
						}
					}
					if c[0] == "/usr/bin/systemd-run" {
						t.Fatal("replaced owner")
					}
				}
				if starts != 1 {
					t.Fatal("not immediately started", starts)
				}
			})
		}
	}
}

// Each transition happens on an independent journal handle while startup is
// paused after launch or during readback. The barrier prevents a scheduling
// accident from turning this into a before/after sequential test.
func TestRuntimeStartupTemporalMatrix(t *testing.T) {
	for _, stage := range []string{"launch", "service-readback", "timer-readback"} {
		for _, action := range []string{"confirm", "helper", "identity", "state", "cancel"} {
			t.Run(stage+"/"+action, func(t *testing.T) {
				l, r, _ := runtimeFixture(t, true)
				f := newManagerFixture(t, r)
				reached := make(chan struct{})
				release := make(chan struct{})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				paused := false
				runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
					match := p == "/usr/bin/systemd-run" && stage == "launch"
					if f.exists && p == "/usr/bin/busctl" && a[6] == "GetAll" {
						match = stage == "service-readback" && strings.HasSuffix(a[len(a)-1], ".Service") || stage == "timer-readback" && strings.HasSuffix(a[len(a)-1], ".Timer")
					}
					if match && !paused {
						paused = true
						close(reached)
						<-release
					}
					return nil, nil, 0, nil, false
				}}
				m := reviewManager(f)
				m.runner = runner
				done := make(chan error, 1)
				go func() { done <- startupFirewall(ctx, l, m) }()
				<-reached
				switch action {
				case "cancel":
					cancel()
				case "helper", "confirm":
					mustCustody(t, runTemporalChild(t, l.root, action))
				default:
					j, e := OpenJournal(l.root + "/journal")
					mustCustody(t, e)
					if action == "confirm" {
						mustCustody(t, j.Confirm(context.Background(), r.ID, func() time.Time { return time.Unix(90, 0) }, func(string) error { return nil }))
					} else {
						if action == "identity" {
							r.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
							r.Unit = "novapanel-fwrevert-" + r.ID
						} else {
							r.State = "confirming"
						}
						mustCustody(t, j.locked(context.Background(), func() error { return j.publish(r) }))
					}
					mustCustody(t, j.Close())
				}
				close(release)
				e := <-done
				if !errors.Is(e, ErrFirewallRecoveryPending) {
					t.Fatal("startup escaped", e)
				}
				if action != "cancel" && errors.Is(e, errManager) {
					t.Fatal("manager verification masked by pending", e)
				}
				if action != "cancel" && !strings.Contains(e.Error(), "journal changed during manager verification") {
					t.Fatal("missed concurrent transition", e)
				}
				for _, c := range f.calls {
					if c[0] == "/usr/bin/systemctl" {
						t.Fatal("startup stopped or repaired owner", c)
					}
				}
				j, x := OpenJournal(l.root + "/journal")
				mustCustody(t, x)
				after, p, x := j.Inspect(context.Background())
				mustCustody(t, x)
				mustCustody(t, j.Close())
				if !p {
					t.Fatal("lost authority")
				}
				want := r.State
				if action == "helper" {
					want = "reverted"
				}
				if action == "confirm" {
					want = "confirmed"
				}
				if after.ID != r.ID || after.State != want {
					t.Fatal("transition evidence lost", after)
				}
			})
		}
	}
}

package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	execpkg "novapanel/internal/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

type managerFixture struct {
	t       *testing.T
	r       JournalRecord
	exists  bool
	calls   [][]string
	service map[string]any
	unit    map[string]any
	timer   map[string]any
}

func variant(sig string, v any) any { return map[string]any{"type": sig, "data": v} }
func managerResponse(sig string, v any) []byte {
	b, _ := json.Marshal(map[string]any{"type": sig, "data": v})
	return b
}
func newManagerFixture(t *testing.T, r JournalRecord) *managerFixture {
	f := &managerFixture{t: t, r: r, service: map[string]any{}, unit: map[string]any{}, timer: map[string]any{}}
	for _, k := range []string{"PartOf", "BindsTo", "Requires", "Requisite", "Conflicts", "PropagatesStopTo", "StopPropagatedFrom", "BoundBy"} {
		f.unit[k] = variant("as", []string{})
	}
	f.unit["LoadState"] = variant("s", "loaded")
	f.unit["Transient"] = variant("b", true)
	f.unit["StartLimitIntervalUSec"] = variant("t", uint64(0))
	for k, v := range map[string]string{"Type": "exec", "User": "root", "Group": "root", "Slice": "system.slice", "KillMode": "control-group", "Restart": "on-failure", "RestartMode": "normal", "ControlGroup": ""} {
		f.service[k] = variant("s", v)
	}
	for k, v := range map[string]bool{"DynamicUser": false, "Delegate": false, "SendSIGKILL": true, "RemainAfterExit": false} {
		f.service[k] = variant("b", v)
	}
	for k, v := range map[string]uint64{"RestartUSec": 5000000, "TimeoutStopUSec": 10000000, "TimeoutStartUSec": ^uint64(0), "RuntimeMaxUSec": ^uint64(0)} {
		f.service[k] = variant("t", v)
	}
	f.service["ExecStart"] = variant("a(sasbttttuii)", []any{[]any{"/usr/sbin/novapanel-agent", []string{"/usr/sbin/novapanel-agent", "fw-revert", productionFirewallRoot + "/snapshots/" + r.ID}, false, 0, 0, 0, 0, 0, 0, 0}})
	for _, k := range []string{"ExecStartPre", "ExecStartPost", "ExecCondition", "ExecStop", "ExecStopPost"} {
		f.service[k] = variant("a(sasbttttuii)", []any{})
	}
	for _, k := range []string{"RestartPreventExitStatus", "RestartForceExitStatus"} {
		f.service[k] = variant("(aiai)", []any{[]int{}, []int{}})
	}
	f.timer["Unit"] = variant("s", r.Unit+".service")
	f.timer["AccuracyUSec"] = variant("t", 1000000)
	f.timer["RandomizedDelayUSec"] = variant("t", 0)
	f.timer["RemainAfterElapse"] = variant("b", false)
	f.timer["TimersCalendar"] = variant("a(sst)", []any{})
	f.timer["TimersMonotonic"] = variant("a(stt)", []any{[]any{"OnActiveUSec", 10000000, 11000000}})
	f.timer["NextElapseUSecMonotonic"] = variant("t", 11000000)
	return f
}
func (f *managerFixture) Run(ctx context.Context, p string, a []string, o execpkg.RunOpts) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, append([]string{p}, a...))
	if _, ok := ctx.Deadline(); !ok {
		f.t.Error("unbounded command")
	}
	if o.MaxOutput != 262145 || !reflect.DeepEqual(o.Env, []string{"LC_ALL=C", "LANG=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}) {
		f.t.Error("unsafe options", o)
	}
	if p == "/usr/bin/systemd-run" {
		f.exists = true
		return nil, nil, 0, nil
	}
	if p != "/usr/bin/busctl" {
		return nil, nil, 0, fmt.Errorf("unexpected program %s", p)
	}
	method := a[6]
	if method == "ListUnitsByNames" {
		rows := []any{}
		if f.exists {
			n := a[len(a)-1]
			rows = append(rows, []any{n, "", "loaded", "inactive", "dead", "", "/org/freedesktop/systemd1/unit/fixture", 0, "", "/"})
		}
		return managerResponse("a(ssssssouso)", []any{rows}), nil, 0, nil
	}
	if method == "GetUnit" {
		return managerResponse("o", []any{"/org/freedesktop/systemd1/unit/fixture"}), nil, 0, nil
	}
	if method == "GetAll" {
		iface := a[len(a)-1]
		props := f.service
		if strings.HasSuffix(iface, "Timer") {
			props = f.timer
		}
		if strings.HasSuffix(iface, "Unit") {
			props = map[string]any{}
			for k, v := range f.unit {
				props[k] = v
			}
			// query identity is recorded by GetUnit immediately before GetAll.
			n := ""
			for i := len(f.calls) - 1; i >= 0; i-- {
				c := f.calls[i]
				if len(c) > 7 && c[7] == "GetUnit" {
					n = c[len(c)-1]
					break
				}
			}
			props["Id"] = variant("s", n)
			props["Names"] = variant("as", []string{n})
			props["ActiveState"] = variant("s", "inactive")
			props["SubState"] = variant("s", "dead")
			if strings.HasSuffix(n, ".timer") {
				props["ActiveState"] = variant("s", "active")
				props["SubState"] = variant("s", "waiting")
			}
		}
		return managerResponse("a{sv}", []any{props}), nil, 0, nil
	}
	return nil, nil, 1, fmt.Errorf("unknown method: %v", a)
}
func TestRuntimeManagerLaunchReadback(t *testing.T) {
	_, r := journalFixture(t)
	r.Deadline = 100
	f := newManagerFixture(t, r)
	m := runtimeManager{runner: f, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
	if e := m.ensure(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	var launch []string
	for _, c := range f.calls {
		if c[0] == "/usr/bin/systemd-run" {
			launch = c
		}
	}
	joined := strings.Join(launch, " ")
	for _, v := range []string{"--system", "--unit=" + r.Unit, "--on-active=10000000us", "--timer-property=AccuracySec=1s", "--timer-property=RemainAfterElapse=no", "Type=exec", "User=root", "Group=root", "KillMode=control-group", "SendSIGKILL=yes", "RemainAfterExit=no", "Restart=on-failure", "RestartSec=5s", "TimeoutStopSec=10s", "TimeoutStartSec=infinity", "RuntimeMaxSec=infinity", "StartLimitIntervalSec=0", "Slice=system.slice", "Delegate=no", "/usr/sbin/novapanel-agent fw-revert " + productionFirewallRoot + "/snapshots/" + r.ID} {
		if !strings.Contains(joined, v) {
			t.Errorf("missing %s: %s", v, joined)
		}
	}
	if len(f.calls) < 10 {
		t.Fatal("missing independent repeated readback", f.calls)
	}
}

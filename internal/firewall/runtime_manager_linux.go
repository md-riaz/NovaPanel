package firewall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/platform"
)

const managerLimit = 256 << 10

var errManager = errors.New("firewall manager verification refused")
var managerObject = regexp.MustCompile(`^/org/freedesktop/systemd1/unit/[A-Za-z0-9_]+$`)

type runtimeManager struct {
	runner    execpkg.Runner
	now       func() time.Time
	monotonic func() (uint64, error)
}

func managerMonotonic() (uint64, error) {
	var ts unix.Timespec
	if e := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); e != nil {
		return 0, e
	}
	if ts.Sec < 0 || uint64(ts.Sec) > math.MaxUint64/1000000 {
		return 0, errManager
	}
	return uint64(ts.Sec)*1000000 + uint64(ts.Nsec)/1000, nil
}
func (m runtimeManager) run(ctx context.Context, p string, a ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if m.runner == nil {
		return nil, errManager
	}
	b, _, code, e := m.runner.Run(ctx, p, a, execpkg.RunOpts{Env: []string{"LC_ALL=C", "LANG=C", "SYSTEMD_PAGER=cat", "SYSTEMD_COLORS=0"}, MaxOutput: managerLimit + 1})
	if e != nil || code != 0 || len(b) > managerLimit || ctx.Err() != nil {
		return nil, errors.Join(errManager, e, ctx.Err())
	}
	return b, nil
}

// decodeManager rejects duplicate keys at every depth before decoding variants.
func decodeManager(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var walk func(int) (any, error)
	walk = func(depth int) (any, error) {
		if depth > 24 {
			return nil, errManager
		}
		tok, e := d.Token()
		if e != nil {
			return nil, e
		}
		switch tok {
		case json.Delim('{'):
			v := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				s, ok := k.(string)
				if !ok {
					return nil, errManager
				}
				if _, ok = v[s]; ok {
					return nil, errManager
				}
				x, e := walk(depth + 1)
				if e != nil {
					return nil, e
				}
				v[s] = x
			}
			_, e = d.Token()
			return v, e
		case json.Delim('['):
			v := []any{}
			for d.More() {
				if len(v) >= 4096 {
					return nil, errManager
				}
				x, e := walk(depth + 1)
				if e != nil {
					return nil, e
				}
				v = append(v, x)
			}
			_, e = d.Token()
			return v, e
		}
		return tok, nil
	}
	v, e := walk(0)
	if e != nil {
		return nil, errManager
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errManager
	}
	return v, nil
}
func typed(v any, sig string) (any, error) {
	o, ok := v.(map[string]any)
	if !ok || len(o) != 2 || o["type"] != sig {
		return nil, errManager
	}
	x, ok := o["data"]
	if !ok || !typedValue(sig, x) {
		return nil, errManager
	}
	return x, nil
}
func typedValue(sig string, v any) bool {
	switch sig {
	case "x", "n", "h":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		bits := 64
		if sig == "n" {
			bits = 16
		}
		if sig == "h" {
			bits = 32
		}
		_, e := strconv.ParseInt(string(n), 10, bits)
		return e == nil
	case "y", "q":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		bits := 8
		if sig == "q" {
			bits = 16
		}
		_, e := strconv.ParseUint(string(n), 10, bits)
		return e == nil
	case "d":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		x, e := strconv.ParseFloat(string(n), 64)
		return e == nil && !math.IsNaN(x) && !math.IsInf(x, 0)
	case "v":
		p, ok := v.(map[string]any)
		if !ok {
			return false
		}
		s, ok := p["type"].(string)
		if !ok {
			return false
		}
		_, e := typed(v, s)
		return e == nil
	case "s", "o", "g":
		_, ok := v.(string)
		return ok
	case "b":
		_, ok := v.(bool)
		return ok
	case "t", "u":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		u, e := strconv.ParseUint(string(n), 10, 64)
		return e == nil && (sig != "u" || u <= math.MaxUint32)
	case "i":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		_, e := strconv.ParseInt(string(n), 10, 32)
		return e == nil
	}
	if strings.HasPrefix(sig, "a{") {
		if sig != "a{sv}" {
			if !strings.HasSuffix(sig, "}") {
				return false
			}
			parts := splitSignature(sig[2 : len(sig)-1])
			if len(parts) != 2 || parts[0] != "s" {
				return false
			}
			o, ok := v.(map[string]any)
			if !ok || len(o) > 4096 {
				return false
			}
			for _, x := range o {
				if !typedValue(parts[1], x) {
					return false
				}
			}
			return true
		}
		o, ok := v.(map[string]any)
		if !ok {
			return false
		}
		for _, x := range o {
			p, ok := x.(map[string]any)
			if !ok {
				return false
			}
			s, ok := p["type"].(string)
			if !ok {
				return false
			}
			if _, e := typed(x, s); e != nil {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(sig, "a") {
		a, ok := v.([]any)
		if !ok || len(a) > 4096 {
			return false
		}
		for _, x := range a {
			if !typedValue(sig[1:], x) {
				return false
			}
		}
		return true
	}
	if strings.HasPrefix(sig, "(") && strings.HasSuffix(sig, ")") {
		a, ok := v.([]any)
		if !ok {
			return false
		}
		parts := splitSignature(sig[1 : len(sig)-1])
		if len(parts) != len(a) {
			return false
		}
		for i, s := range parts {
			if !typedValue(s, a[i]) {
				return false
			}
		}
		return true
	}
	return false
}
func splitSignature(s string) []string {
	var out []string
	for len(s) > 0 {
		n := 1
		for n < len(s) && s[n-1] == 'a' {
			n++
		}
		if n > len(s) {
			return nil
		}
		if s[n-1] == '(' || s[n-1] == '{' {
			open := s[n-1]
			close := byte(')')
			if open == '{' {
				close = '}'
			}
			depth := 1
			for n < len(s) && depth > 0 {
				if s[n] == open {
					depth++
				}
				if s[n] == close {
					depth--
				}
				n++
			}
			if depth != 0 {
				return nil
			}
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}
func (m runtimeManager) call(ctx context.Context, path, iface, method, sig string, args ...string) (any, error) {
	a := []string{"--system", "--json=short", "call", "org.freedesktop.systemd1", path, iface, method}
	if sig != "" {
		a = append(a, sig)
		a = append(a, args...)
	}
	b, e := m.run(ctx, platform.Busctl, a...)
	if e != nil {
		return nil, e
	}
	return decodeManager(b)
}
func (m runtimeManager) query(ctx context.Context, name string) (map[string]any, error) {
	v, e := m.call(ctx, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "ListUnitsByNames", "as", "1", name)
	if e != nil {
		return nil, e
	}
	o, ok := v.(map[string]any)
	if !ok || o["type"] != "a(ssssssouso)" {
		return nil, errManager
	}
	data, ok := o["data"].([]any)
	if !ok || len(data) != 1 || !typedValue("a(ssssssouso)", data[0]) {
		return nil, errManager
	}
	rows := data[0].([]any)
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 || rows[0].([]any)[0] != name {
		return nil, errManager
	}
	if rows[0].([]any)[2] == "not-found" {
		return nil, nil
	}
	if rows[0].([]any)[2] != "loaded" {
		return nil, errManager
	}
	v, e = m.call(ctx, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "GetUnit", "s", name)
	if e != nil {
		return nil, e
	}
	o, ok = v.(map[string]any)
	if !ok || o["type"] != "o" {
		return nil, errManager
	}
	data, ok = o["data"].([]any)
	if !ok || len(data) != 1 {
		return nil, errManager
	}
	path, ok := data[0].(string)
	if !ok || !managerObject.MatchString(path) || rows[0].([]any)[6] != path {
		return nil, errManager
	}
	all := map[string]any{}
	kind := "Service"
	if strings.HasSuffix(name, ".timer") {
		kind = "Timer"
	}
	for _, iface := range []string{"Unit", kind} {
		v, e = m.call(ctx, path, "org.freedesktop.DBus.Properties", "GetAll", "s", "org.freedesktop.systemd1."+iface)
		if e != nil {
			return nil, e
		}
		o, ok = v.(map[string]any)
		if !ok || o["type"] != "a{sv}" {
			return nil, errManager
		}
		data, ok = o["data"].([]any)
		if !ok || len(data) != 1 {
			return nil, errManager
		}
		if !typedValue("a{sv}", data[0]) {
			return nil, errManager
		}
		for k, x := range data[0].(map[string]any) {
			if _, ok := all[k]; ok {
				return nil, errManager
			}
			all[k] = x
		}
	}
	return all, nil
}
func property(p map[string]any, k, sig string) (any, error) { return typed(p[k], sig) }
func equalProperty(p map[string]any, k, sig string, want any) bool {
	x, e := property(p, k, sig)
	if e != nil {
		return false
	}
	b, _ := json.Marshal(want)
	v, _ := decodeManager(b)
	return reflect.DeepEqual(x, v)
}
func verifyUnit(p map[string]any, name string) error {
	if !equalProperty(p, "Id", "s", name) || !equalProperty(p, "Names", "as", []string{name}) || !equalProperty(p, "LoadState", "s", "loaded") || !equalProperty(p, "Transient", "b", true) {
		return errManager
	}
	for _, k := range []string{"PartOf", "BindsTo", "Requires", "Requisite", "Conflicts", "PropagatesStopTo", "StopPropagatedFrom", "BoundBy"} {
		x, e := property(p, k, "as")
		if e != nil {
			return e
		}
		for _, v := range x.([]any) {
			if v == "novapanel-agent.service" || v == "novapanel-web.service" {
				return errManager
			}
		}
	}
	return nil
}
func verifyService(p map[string]any, r JournalRecord) error {
	if e := verifyUnit(p, r.Unit+".service"); e != nil {
		return e
	}
	for k, v := range map[string]string{"Type": "exec", "User": "root", "Group": "root", "Slice": "system.slice", "KillMode": "control-group", "Restart": "on-failure", "RestartMode": "normal"} {
		if !equalProperty(p, k, "s", v) {
			return errManager
		}
	}
	for k, v := range map[string]bool{"DynamicUser": false, "Delegate": false, "SendSIGKILL": true, "RemainAfterExit": false} {
		if !equalProperty(p, k, "b", v) {
			return errManager
		}
	}
	for k, v := range map[string]uint64{"RestartUSec": 5000000, "TimeoutStopUSec": 10000000, "TimeoutStartUSec": math.MaxUint64, "RuntimeMaxUSec": math.MaxUint64, "StartLimitIntervalUSec": 0} {
		if !equalProperty(p, k, "t", v) {
			return errManager
		}
	}
	for _, k := range []string{"RestartPreventExitStatus", "RestartForceExitStatus"} {
		if !equalProperty(p, k, "(aiai)", []any{[]int{}, []int{}}) {
			return errManager
		}
	}
	for _, k := range []string{"ExecStartPre", "ExecStartPost", "ExecCondition", "ExecStop", "ExecStopPost"} {
		if !equalProperty(p, k, "a(sasbttttuii)", []any{}) {
			return errManager
		}
	}
	x, e := property(p, "ExecStart", "a(sasbttttuii)")
	if e != nil {
		return e
	}
	cmd := x.([]any)
	if len(cmd) != 1 {
		return errManager
	}
	fields := cmd[0].([]any)
	if fields[0] != "/usr/sbin/novapanel-agent" || fields[2] != false {
		return errManager
	}
	b, _ := json.Marshal([]string{"/usr/sbin/novapanel-agent", "fw-revert", productionFirewallRoot + "/snapshots/" + r.ID})
	argv, _ := decodeManager(b)
	if !reflect.DeepEqual(fields[1], argv) {
		return errManager
	}
	active, e := property(p, "ActiveState", "s")
	if e != nil {
		return e
	}
	sub, e := property(p, "SubState", "s")
	if e != nil {
		return e
	}
	cg, e := property(p, "ControlGroup", "s")
	if e != nil {
		return e
	}
	switch active {
	case "inactive":
		if sub != "dead" || cg != "" {
			return errManager
		}
	case "activating", "active":
		if cg != "/system.slice/"+r.Unit+".service" {
			return errManager
		}
		if sub != "start" && sub != "running" && sub != "auto-restart" && sub != "start-post" {
			return errManager
		}
	default:
		return errManager
	}
	return nil
}
func uintProperty(p map[string]any, k string) (uint64, error) {
	x, e := property(p, k, "t")
	if e != nil {
		return 0, e
	}
	return strconv.ParseUint(string(x.(json.Number)), 10, 64)
}
func verifyTimer(p map[string]any, r JournalRecord, remaining, start, end uint64) error {
	if e := verifyUnit(p, r.Unit+".timer"); e != nil {
		return e
	}
	for k, v := range map[string]string{"Unit": r.Unit + ".service", "ActiveState": "active", "SubState": "waiting"} {
		if !equalProperty(p, k, "s", v) {
			return errManager
		}
	}
	if !equalProperty(p, "AccuracyUSec", "t", 1000000) || !equalProperty(p, "RandomizedDelayUSec", "t", 0) || !equalProperty(p, "RemainAfterElapse", "b", false) || !equalProperty(p, "TimersCalendar", "a(sst)", []any{}) {
		return errManager
	}
	x, e := property(p, "TimersMonotonic", "a(stt)")
	if e != nil {
		return e
	}
	rows := x.([]any)
	if len(rows) != 1 {
		return errManager
	}
	row := rows[0].([]any)
	if row[0] != "OnActiveUSec" {
		return errManager
	}
	interval, e := strconv.ParseUint(string(row[1].(json.Number)), 10, 64)
	if e != nil || interval == 0 {
		return errManager
	}
	next, e := uintProperty(p, "NextElapseUSecMonotonic")
	if e != nil || end < start || next <= end {
		return errManager
	}
	bound := remaining + 1000000
	if bound < remaining || math.MaxUint64-bound < end {
		return errManager
	}
	if next > end+bound || interval > remaining+1000000+(end-start) {
		return errManager
	}
	if row[2] != json.Number(strconv.FormatUint(next, 10)) {
		return errManager
	}
	return nil
}
func remainingMicros(deadline int64, now time.Time) (uint64, error) {
	s := now.Unix()
	if s < 0 || s > 253402300799 || deadline <= 0 || deadline > 253402300799 {
		return 0, errManager
	}
	if s >= deadline {
		return 0, nil
	}
	seconds := uint64(deadline - s)
	if seconds > math.MaxUint64/1000000 {
		return 0, errManager
	}
	return seconds*1000000 - uint64(now.Nanosecond())/1000, nil
}
func (m runtimeManager) ensure(ctx context.Context, r JournalRecord) error {
	if !validJournal(r) || m.now == nil || m.monotonic == nil {
		return errManager
	}
	start, e := m.monotonic()
	if e != nil {
		return e
	}
	service, e := m.query(ctx, r.Unit+".service")
	if e != nil {
		return e
	}
	timer, e := m.query(ctx, r.Unit+".timer")
	if e != nil {
		return e
	}
	remaining, e := remainingMicros(r.Deadline, m.now())
	if e != nil {
		return e
	}
	future := remaining > 0 && r.State != "reverting"
	if service == nil {
		if timer != nil {
			return errManager
		}
		a := []string{"--system", "--unit=" + r.Unit}
		if future {
			a = append(a, "--on-active="+strconv.FormatUint(remaining, 10)+"us", "--timer-property=AccuracySec=1s", "--timer-property=RemainAfterElapse=no")
		}
		for _, p := range []string{"Type=exec", "User=root", "Group=root", "KillMode=control-group", "SendSIGKILL=yes", "RemainAfterExit=no", "Restart=on-failure", "RestartSec=5s", "TimeoutStopSec=10s", "TimeoutStartSec=infinity", "RuntimeMaxSec=infinity", "StartLimitIntervalSec=0", "Slice=system.slice", "Delegate=no"} {
			a = append(a, "--property="+p)
		}
		a = append(a, "--", "/usr/sbin/novapanel-agent", "fw-revert", productionFirewallRoot+"/snapshots/"+r.ID)
		// Failure has ambiguous external effects. Readback, never rollback, decides.
		_, _ = m.run(ctx, platform.SystemdRun, a...)
	} else {
		if e = verifyService(service, r); e != nil {
			return e
		}
		if !future || timer == nil {
			if timer != nil {
				return errManager
			}
			if _, e = m.run(ctx, platform.Systemctl, "--system", "start", "--no-block", r.Unit+".service"); e != nil {
				return e
			}
			future = false
		}
	}
	service, e = m.query(ctx, r.Unit+".service")
	if e != nil || service == nil {
		return errors.Join(errManager, e)
	}
	timer, e = m.query(ctx, r.Unit+".timer")
	if e != nil {
		return e
	}
	if e = verifyService(service, r); e != nil {
		return e
	}
	end, e := m.monotonic()
	if e != nil {
		return e
	}
	if future {
		if e = verifyTimer(timer, r, remaining, start, end); e != nil {
			return e
		}
	} else {
		if timer != nil {
			return errManager
		}
		active, _ := property(service, "ActiveState", "s")
		if active != "active" && active != "activating" {
			return errManager
		}
	}
	again, e := m.query(ctx, r.Unit+".service")
	if e != nil || !reflect.DeepEqual(service, again) {
		return errors.Join(errManager, e)
	}
	again, e = m.query(ctx, r.Unit+".timer")
	if e != nil || !reflect.DeepEqual(timer, again) {
		return errors.Join(errManager, e)
	}
	return nil
}
func (m runtimeManager) stop(ctx context.Context, stem string) error {
	r := JournalRecord{Version: 1, ID: strings.TrimPrefix(stem, "novapanel-fwrevert-"), Unit: stem, Deadline: 1, SnapshotSHA256: strings.Repeat("a", 64), State: "confirmed"}
	if !validJournal(r) {
		return errManager
	}
	if _, e := m.run(ctx, platform.Systemctl, "--system", "stop", stem+".timer"); e != nil {
		return e
	}
	p, e := m.query(ctx, stem+".timer")
	if e != nil {
		return e
	}
	if p == nil {
		return nil
	}
	if !equalProperty(p, "Id", "s", stem+".timer") || !equalProperty(p, "ActiveState", "s", "inactive") {
		return fmt.Errorf("%w: timer still active", errManager)
	}
	return nil
}

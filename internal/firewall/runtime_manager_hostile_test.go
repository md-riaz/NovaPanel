package firewall

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRuntimeManagerRejectsUnsafeProperties(t *testing.T) {
	_, r := journalFixture(t)
	for _, group := range []string{"service", "unit", "timer"} {
		base := newManagerFixture(t, r)
		props := base.service
		if group == "unit" {
			props = base.unit
		}
		if group == "timer" {
			props = base.timer
		}
		for key := range props {
			for _, bad := range []string{"missing", "wrong-type", "wrong-value"} {
				t.Run(group+"/"+key+"/"+bad, func(t *testing.T) {
					f := newManagerFixture(t, r)
					f.exists = true
					p := f.service
					if group == "unit" {
						p = f.unit
					}
					if group == "timer" {
						p = f.timer
					}
					switch bad {
					case "missing":
						delete(p, key)
					case "wrong-type":
						p[key] = variant("i", 1)
					case "wrong-value":
						v := p[key].(map[string]any)
						switch v["type"] {
						case "s":
							p[key] = variant("s", "forged")
						case "b":
							p[key] = variant("b", !v["data"].(bool))
						case "t":
							p[key] = variant("t", 123)
						case "as":
							p[key] = variant("as", []string{"novapanel-agent.service"})
						default:
							p[key] = variant("s", "forged")
						}
					}
					m := runtimeManager{runner: f, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
					if e := m.ensure(context.Background(), r); e == nil {
						t.Fatal("unsafe manager admitted")
					}
					for _, c := range f.calls {
						if c[0] == "/usr/bin/systemd-run" || c[0] == "/usr/bin/systemctl" {
							t.Fatal("conflicting unit changed", c)
						}
					}
				})
			}
		}
	}
}
func TestManagerDecoderRejectsHostileJSON(t *testing.T) {
	for _, b := range []string{`{"type":"s","type":"s","data":"x"}`, `{"type":"a{sv}","data":[{"a":{"type":"b","data":false,"data":true}}]}`, `{"type":"s","data":"x"} true`, `{"type":"s","data":`} {
		if _, e := decodeManager([]byte(b)); e == nil {
			t.Fatal(b)
		}
	}
}
func TestRuntimeManagerRemainder(t *testing.T) {
	for _, tc := range []struct {
		deadline int64
		now      time.Time
		want     uint64
		bad      bool
	}{{100, time.Unix(90, 1), 10000000, false}, {100, time.Unix(99, 999999999), 1, false}, {100, time.Unix(100, 0), 0, false}, {253402300799, time.Unix(0, 0), 253402300799000000, false}, {100, time.Unix(-1, 0), 0, true}, {253402300800, time.Unix(0, 0), 0, true}} {
		got, e := remainingMicros(tc.deadline, tc.now)
		if (e != nil) != tc.bad || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
}
func TestRuntimeManagerRejectsSchedule(t *testing.T) {
	_, r := journalFixture(t)
	for _, tc := range []struct {
		name, key, sig string
		value          any
	}{{"late", "NextElapseUSecMonotonic", "t", 999999999}, {"elapsed", "NextElapseUSecMonotonic", "t", 1000000}, {"extra", "TimersMonotonic", "a(stt)", []any{[]any{"OnActiveUSec", 10000000, 11000000}, []any{"OnBootUSec", 10000000, 11000000}}}, {"calendar", "TimersCalendar", "a(sst)", []any{[]any{"OnCalendar", "daily", 11000000}}}, {"wrong-target", "Unit", "s", "other.service"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagerFixture(t, r)
			f.exists = true
			f.timer[tc.key] = variant(tc.sig, tc.value)
			m := runtimeManager{runner: f, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
			if e := m.ensure(context.Background(), r); e == nil {
				t.Fatal("unsafe schedule")
			}
		})
	}
}
func TestManagerTypedUnrelatedProperty(t *testing.T) {
	_, r := journalFixture(t)
	f := newManagerFixture(t, r)
	f.service["Unrelated"] = variant("a(ss)", []any{[]any{"a", "b"}})
	m := runtimeManager{runner: f, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
	if e := m.ensure(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	b := managerResponse("a{sv}", []any{map[string]any{"x": variant("t", json.Number("18446744073709551616"))}})
	v, e := decodeManager(b)
	if e != nil {
		t.Fatal(e)
	}
	data := v.(map[string]any)["data"].([]any)[0]
	if typedValue("a{sv}", data) {
		t.Fatal("overflow")
	}
}

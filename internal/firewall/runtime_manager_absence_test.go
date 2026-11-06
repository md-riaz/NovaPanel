package firewall

import (
	"context"
	"testing"
	"time"
)

func TestRuntimeManagerTypedNotFoundRows(t *testing.T) {
	_, r := journalFixture(t)
	f := newManagerFixture(t, r)
	runner := interceptManager{base: f, hook: func(ctx context.Context, p string, a []string) ([]byte, []byte, int, error, bool) {
		if p == "/usr/bin/busctl" && a[6] == "ListUnitsByNames" && !f.exists {
			return managerResponse("a(ssssssouso)", []any{[]any{[]any{a[len(a)-1], "", "not-found", "inactive", "dead", "", "/org/freedesktop/systemd1/unit/fixture", 0, "", "/"}}}), nil, 0, nil, true
		}
		return nil, nil, 0, nil, false
	}}
	m := runtimeManager{runner: runner, now: func() time.Time { return time.Unix(90, 0) }, monotonic: func() (uint64, error) { return 1000000, nil }}
	p, e := m.query(context.Background(), r.Unit+".service")
	if e != nil || p != nil {
		t.Fatal("typed not-found is not absence", p, e)
	}
}

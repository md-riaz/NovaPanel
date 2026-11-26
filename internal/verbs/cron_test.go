package verbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/store"
)

const testCronSite = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
const testCronID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"

type cronFake struct {
	calls []string
	fail  string
}

func (f *cronFake) Run(_ context.Context, p string, a []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	call := p + " " + strings.Join(a, " ")
	f.calls = append(f.calls, call)
	if f.fail != "" && strings.Contains(call, f.fail) {
		f.fail = ""
		return nil, nil, 1, errors.New("failed")
	}
	return nil, nil, 0, nil
}
func TestCronRenderAndValidation(t *testing.T) {
	v := cronCreateArgs{SiteID: testCronSite, Schedule: "daily", Argv: []string{"/bin/echo", `hello "world"`}}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
	row := store.CronJob{ID: testCronID, SiteID: testCronSite, Schedule: v.Schedule, ArgvJSON: `["/bin/echo","hello \\\"world\\\""]`, UnitName: cronUnit(testCronSite, testCronID)}
	service, timer, err := cronRender(row, store.Site{Name: "example", Docroot: "/srv/sites/example/public"}, 123)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Type=oneshot", "User=example", "Group=123", "WorkingDirectory=/srv/sites/example/public", `ExecStart="/bin/echo"`} {
		if !strings.Contains(service, s) {
			t.Fatalf("missing %q in %s", s, service)
		}
	}
	if !strings.Contains(timer, "OnCalendar=daily") {
		t.Fatal(timer)
	}
	for _, bad := range []string{"relative", "/bin/%s", "/bin/$x", "/bin/x\n", "/bin/x\x00"} {
		v.Argv = []string{bad}
		if v.Validate() == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func TestCronApplyRollback(t *testing.T) {
	dir := t.TempDir()
	row := store.CronJob{ID: testCronID, SiteID: testCronSite, UnitName: cronUnit(testCronSite, testCronID)}
	fake := &cronFake{fail: "enable --now"}
	if err := cronApply(context.Background(), fake, dir, row, "service", "timer"); err == nil {
		t.Fatal("expected failure")
	}
	if len(fake.calls) != 5 || !strings.Contains(fake.calls[0], "systemd-analyze verify ") || !strings.Contains(fake.calls[1], "daemon-reload") || !strings.Contains(fake.calls[2], "enable --now") || !strings.Contains(fake.calls[3], "disable --now") || !strings.Contains(fake.calls[4], "daemon-reload") {
		t.Fatal(fake.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, row.UnitName+".timer")); !os.IsNotExist(err) {
		t.Fatalf("timer survived: %v", err)
	}
	row.Enabled = true
	fake = &cronFake{fail: "enable --now"}
	if err := cronApply(context.Background(), fake, dir, row, "service", "timer"); err == nil {
		t.Fatal("expected failure")
	}
	if len(fake.calls) != 6 || !strings.Contains(fake.calls[5], "enable --now") {
		t.Fatal(fake.calls)
	}
	if b, err := os.ReadFile(filepath.Join(dir, row.UnitName+".timer")); err != nil || string(b) != "timer" {
		t.Fatalf("timer not restored: %s %v", b, err)
	}
}

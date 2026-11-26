package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
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
func TestCronCreateWriteAhead(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateSite(ctx, store.Site{ID: testCronSite, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: "domain", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, testCronSite, "active"); err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(s, filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = s
	a.Jobs = jobs.NewQueue(s, seal, log)
	a.Exec = &cronFake{}
	if err = a.Jobs.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := cronCreate(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "owner", Role: "admin"}), a, &cronCreateArgs{SiteID: testCronSite, Schedule: "daily", Argv: []string{"/bin/echo", "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	job := result.(jobs.EnqueueResult).Job
	var input cronCreateArgs
	if err = json.Unmarshal(job.Args, &input); err != nil {
		t.Fatal(err)
	}
	row, err := s.CronByID(ctx, input.CronID)
	if err != nil || row.Enabled || row.UnitName != cronUnit(testCronSite, input.CronID) || row.ArgvJSON != `["/bin/echo","hello"]` {
		t.Fatalf("reservation: %+v %v", row, err)
	}
}
func TestCronRegistryAndBadSchedule(t *testing.T) {
	for _, name := range []string{"cron.create", "cron.delete", "cron.enable", "cron.disable"} {
		v := rpc.Registry[name]
		if v.MinRole != rpc.Admin || v.Scope != "sites:write" || !v.Slow || v.MFA || v.Exec == nil {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	list := rpc.Registry["cron.list"]
	if list.MinRole != rpc.ReadOnly || !list.ReadOnly || list.Scope != "sites:read" || list.MFA || list.Slow {
		t.Fatalf("list: %+v", list)
	}
	for _, schedule := range []string{"--help", "--version", "-x"} {
		_, err := cronCreate(context.Background(), agent.New(), &cronCreateArgs{SiteID: testCronSite, Schedule: schedule, Argv: []string{"/bin/echo"}})
		var coded *rpc.CodedError
		if !errors.As(err, &coded) || coded.Code != "invalid_argument" {
			t.Fatalf("schedule %q: %v", schedule, err)
		}
	}
	a := agent.New()
	f := &cronFake{fail: "calendar -- bad"}
	a.Exec = f
	_, err := cronCreate(context.Background(), a, &cronCreateArgs{SiteID: testCronSite, Schedule: "bad", Argv: []string{"/bin/echo"}})
	var coded *rpc.CodedError
	if !errors.As(err, &coded) || coded.Code != "invalid_argument" || len(f.calls) != 1 || f.calls[0] != "/usr/bin/systemd-analyze calendar -- bad" {
		t.Fatalf("bad schedule: %v %v", err, f.calls)
	}
}
func TestCronTeardownAndLookup(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateSite(ctx, store.Site{ID: testCronSite, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: "domain", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	row := store.CronJob{ID: testCronID, SiteID: testCronSite, Schedule: "daily", ArgvJSON: `["/bin/echo","hi"]`, UnitName: cronUnit(testCronSite, testCronID), Created: 1}
	if err = s.Tx(ctx, func(tx *sql.Tx) error { return s.InsertCronTx(ctx, tx, row) }); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = s
	args := &cronIDArgs{CronID: testCronID}
	key, err := (cronState{a}).resolve(ctx, args)
	if err != nil || key != "site:"+testCronSite || args.SiteID != testCronSite {
		t.Fatalf("resolve: %s %+v %v", key, args, err)
	}
	got, err := cronList(ctx, a, &siteIDArgs{testCronSite})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(raw), `"argv":["/bin/echo","hi"]`) {
		t.Fatalf("list: %s %v", raw, err)
	}
	dir := t.TempDir()
	own := row.UnitName + ".timer"
	other := cronUnit("01ARZ3NDEKTSV4RRFFQ69G5FAX", testCronID) + ".timer"
	orphan := cronUnit(testCronSite, "01ARZ3NDEKTSV4RRFFQ69G5FAX") + ".service"
	for _, name := range []string{own, other, orphan} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err = (cronState{a}).teardown(ctx, dir, testCronSite); err != nil {
		t.Fatal(err)
	}
	if err = (cronState{a}).teardown(ctx, dir, testCronSite); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, own)); !os.IsNotExist(err) {
		t.Fatalf("own unit remains: %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, orphan)); !os.IsNotExist(err) {
		t.Fatalf("orphan remains: %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, other)); err != nil {
		t.Fatalf("foreign unit removed: %v", err)
	}
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

type cronMissing struct {
	calls        []string
	inspectFails bool
}

func (f *cronMissing) Run(_ context.Context, p string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, p+" "+strings.Join(args, " "))
	if args[0] == "show" && !f.inspectFails {
		return []byte("not-found\n"), nil, 0, nil
	}
	return nil, nil, 1, errors.New("failed")
}
func TestCronDisableMissing(t *testing.T) {
	f := &cronMissing{}
	if err := cronDisable(context.Background(), f, "missing.timer", "missing.service"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 || !strings.Contains(f.calls[0], "disable --now") {
		t.Fatal(f.calls)
	}
	f = &cronMissing{inspectFails: true}
	if err := cronDisable(context.Background(), f, "missing.timer"); err == nil {
		t.Fatal("inspection failure was ignored")
	}
}
func TestCronApplyCompletesPartialInstall(t *testing.T) {
	dir := t.TempDir()
	row := store.CronJob{ID: testCronID, SiteID: testCronSite, UnitName: cronUnit(testCronSite, testCronID)}
	if err := os.WriteFile(filepath.Join(dir, row.UnitName+".service"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	f := &cronFake{}
	if err := cronApply(context.Background(), f, dir, row, "new service", "new timer"); err != nil {
		t.Fatal(err)
	}
	for suffix, want := range map[string]string{".service": "new service", ".timer": "new timer"} {
		b, err := os.ReadFile(filepath.Join(dir, row.UnitName+suffix))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %s %v", suffix, b, err)
		}
	}
	if len(f.calls) != 3 || !strings.Contains(f.calls[0], "systemd-analyze verify ") || !strings.Contains(f.calls[1], "daemon-reload") || !strings.Contains(f.calls[2], "enable --now") {
		t.Fatal(f.calls)
	}
}
func TestCronReplayAfterInstallFailure(t *testing.T) {
	for _, stage := range []string{"systemd-analyze verify", "daemon-reload", "enable --now"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			row := store.CronJob{ID: testCronID, SiteID: testCronSite, UnitName: cronUnit(testCronSite, testCronID)}
			f := &cronFake{fail: stage}
			if err := cronApply(context.Background(), f, dir, row, "service", "timer"); err == nil {
				t.Fatal("expected failure")
			}
			if err := cronApply(context.Background(), f, dir, row, "service", "timer"); err != nil {
				t.Fatal(err)
			}
			for suffix, want := range map[string]string{".service": "service", ".timer": "timer"} {
				b, err := os.ReadFile(filepath.Join(dir, row.UnitName+suffix))
				if err != nil || string(b) != want {
					t.Fatalf("%s: %s %v", suffix, b, err)
				}
			}
		})
	}
}

type cronCrash struct{ at string }

func (f *cronCrash) Run(_ context.Context, p string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if strings.Contains(p+" "+strings.Join(args, " "), f.at) {
		panic("simulated crash")
	}
	return nil, nil, 0, nil
}
func TestCronReplayAfterCrash(t *testing.T) {
	for _, stage := range []string{"systemd-analyze verify", "daemon-reload", "enable --now"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			row := store.CronJob{ID: testCronID, SiteID: testCronSite, UnitName: cronUnit(testCronSite, testCronID)}
			func() {
				defer func() {
					if recover() == nil {
						t.Fatal("expected crash")
					}
				}()
				_ = cronApply(context.Background(), &cronCrash{at: stage}, dir, row, "service", "timer")
			}()
			if err := cronApply(context.Background(), &cronFake{}, dir, row, "service", "timer"); err != nil {
				t.Fatal(err)
			}
			for suffix, want := range map[string]string{".service": "service", ".timer": "timer"} {
				b, err := os.ReadFile(filepath.Join(dir, row.UnitName+suffix))
				if err != nil || string(b) != want {
					t.Fatalf("%s: %s %v", suffix, b, err)
				}
			}
		})
	}
}
func TestCronRollbackFailureIsCoded(t *testing.T) {
	row := store.CronJob{ID: testCronID, SiteID: testCronSite, UnitName: cronUnit(testCronSite, testCronID)}
	err := cronApply(context.Background(), &cronMissing{inspectFails: true}, t.TempDir(), row, "service", "timer")
	if err == nil || !strings.Contains(err.Error(), "rollback divergence") {
		t.Fatalf("expected rollback divergence: %v", err)
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

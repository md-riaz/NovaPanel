package php

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fmt"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpcerr"
)

type baselineRunner struct{ dir string }

func (r baselineRunner) Run(_ context.Context, bin string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	if bin == "/usr/sbin/php-fpm8.3" {
		return nil, []byte(fmt.Sprintf("[ERROR] failed to parse configuration file '%s'", filepath.Join(r.dir, "www.conf"))), 78, nil
	}
	return nil, nil, 0, nil
}
func TestBaselineForeignFailureRestoresCommitted(t *testing.T) {
	dir := testPoolDir(t)
	if err := os.WriteFile(filepath.Join(dir, "www.conf"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	old := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}
	next := old
	next.MaxChildren = 4
	a := PoolApplier{Dir: dir, Runner: baselineRunner{dir}, Probe: func(context.Context) error { return nil }}
	err := a.ApplyCommitted(context.Background(), "demo", next, old)
	var coded *rpcerr.CodedError
	if !errors.As(err, &coded) || coded.Code != "precondition_failed" || !strings.Contains(err.Error(), "www.conf") {
		t.Fatalf("wrong attribution: %v", err)
	}
	want, _ := renderState("demo", old)
	got, _ := os.ReadFile(filepath.Join(dir, "novapanel-demo.conf"))
	if string(got) != string(want) {
		t.Fatal("committed state not restored")
	}
}

func TestBaselineExecutionErrorIsNotForeign(t *testing.T) {
	dir := testPoolDir(t)
	if err := os.WriteFile(filepath.Join(dir, "www.conf"), []byte("operator"), 0600); err != nil {
		t.Fatal(err)
	}
	r := &poolRunner{hook: func(_ context.Context, _ int, args []string) error {
		if args[0] == "-t" {
			return errors.New("failed in '" + filepath.Join(dir, "www.conf") + "'")
		}
		return nil
	}}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	old := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	next := old
	next.Exists = true
	next.MaxChildren = 3
	err := a.ApplyCommitted(context.Background(), "demo", next, old)
	var coded *rpcerr.CodedError
	if err == nil || (errors.As(err, &coded) && coded.Code == "precondition_failed") {
		t.Fatalf("misattributed execution failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "novapanel-demo.conf")); !os.IsNotExist(err) {
		t.Fatalf("committed absence not restored: %v", err)
	}
}

func TestBaselineRealDiagnosticAttribution(t *testing.T) {
	dir := testPoolDir(t)
	name := "www.conf"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := poolRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	a := PoolApplier{Dir: dir}
	path := filepath.Join(dir, name)
	for _, tc := range []struct {
		name, diagnostic string
		attributed       bool
	}{
		{"real syntax", fmt.Sprintf("[14-Nov-2026 05:43:06] ERROR: [%s:2] unknown entry 'invalid_directive'\n[14-Nov-2026 05:43:06] ERROR: FPM initialization failed", path), true},
		{"unrelated mention", "[14-Nov-2026 05:43:06] ERROR: failed to open error_log (" + path + ")", false},
		{"similar path", "[14-Nov-2026 05:43:06] ERROR: [" + path + ".other:2] unknown entry", false},
		{"missing line", "[14-Nov-2026 05:43:06] ERROR: [" + path + "] unknown entry", false},
		{"forged prefix", "other ERROR: [" + path + ":2] unknown entry", false},
		{"semantic without source", "[14-Nov-2026 05:43:06] ERROR: [pool www] pm.max_children must be a positive value", false},
		{"managed file", "[14-Nov-2026 05:43:06] ERROR: [" + filepath.Join(dir, "novapanel-demo.conf") + ":2] unknown entry", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := a.baselineError(root, &fpmTestError{stderr: tc.diagnostic})
			var coded *rpcerr.CodedError
			got := errors.As(err, &coded) && coded.Code == "precondition_failed"
			if got != tc.attributed {
				t.Fatalf("attributed = %v, error %v", got, err)
			}
		})
	}
}

type poolRunner struct {
	calls int
	hook  func(context.Context, int, []string) error
}

func (r *poolRunner) Run(ctx context.Context, _ string, args []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	r.calls++
	if r.hook != nil {
		if err := r.hook(ctx, r.calls, args); err != nil {
			return nil, nil, 0, err
		}
	}
	return nil, nil, 0, nil
}
func TestCandidatePreservesMaster(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pool.d")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	master := "[global]\nerror_log = /custom/log\ninclude=" + dir + "/*.conf\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "php-fpm.conf"), []byte(master), 0600); err != nil {
		t.Fatal(err)
	}
	r := &poolRunner{hook: func(_ context.Context, n int, args []string) error {
		if n != 1 {
			return nil
		}
		b, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return err
		}
		if !strings.Contains(string(b), "error_log = /custom/log") {
			t.Errorf("lost master config: %s", b)
		}
		return nil
	}}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	if err := a.ApplyCommitted(context.Background(), "demo", PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}, PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}); err != nil {
		t.Fatal(err)
	}
}

func TestRealFPMBaselineAttribution(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/php-fpm8.3"); os.IsNotExist(err) {
		t.Skip("php-fpm8.3 unavailable")
	}
	dir := testPoolDir(t)
	if err := os.WriteFile(filepath.Join(dir, "www.conf"), []byte("[www]\ninvalid_directive = foo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := poolRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	a := PoolApplier{Dir: dir, Runner: runner.Real{}}
	err = a.baselineError(root, a.testCandidate(context.Background(), root, "", nil))
	var coded *rpcerr.CodedError
	if !errors.As(err, &coded) || coded.Code != "precondition_failed" || !strings.Contains(err.Error(), "www.conf") {
		t.Fatalf("real FPM error not attributed: %v", err)
	}
}

func TestRealFPMCandidate(t *testing.T) {
	bin := "/usr/sbin/php-fpm8.3"
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skip("php-fpm8.3 unavailable")
	}
	dir := filepath.Join(t.TempDir(), "pool.d")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	master := "[global]\nerror_log = /dev/null\ninclude=" + dir + "/*.conf\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "php-fpm.conf"), []byte(master), 0600); err != nil {
		t.Fatal(err)
	}
	pool := "[www]\nuser = www-data\ngroup = www-data\nlisten = /tmp/novapanel-test-unused.sock\npm = ondemand\npm.max_children = 2\n"
	if err := os.WriteFile(filepath.Join(dir, "www.conf"), []byte(pool), 0600); err != nil {
		t.Fatal(err)
	}
	r := &poolRunner{hook: func(_ context.Context, n int, args []string) error {
		if n == 1 {
			out, stderr, code, err := (runner.Real{}).Run(context.Background(), bin, args, runner.RunOpts{})
			if err != nil || code != 0 {
				t.Errorf("FPM: %s %s (exit %d)", out, stderr, code)
				return errors.New("FPM validation failed")
			}
		}
		return nil
	}}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	if err := a.ApplyCommitted(context.Background(), "demo", PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}); err != nil {
		t.Fatal(err)
	}
}

func testPoolDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pool.d")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "php-fpm.conf"), []byte("[global]\ninclude="+dir+"/*.conf\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPoolCommittedRecovery(t *testing.T) {
	ctx := context.Background()
	dir := testPoolDir(t)
	r := &poolRunner{}
	a := PoolApplier{Dir: dir, Runner: r, Probe: func(context.Context) error { return nil }}
	old := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}
	next := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 4}
	absent := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "novapanel-demo.conf")
	original, _ := os.ReadFile(path)
	r.hook = func(_ context.Context, n int, args []string) error {
		if n == 4 {
			got, _ := os.ReadFile(path)
			if string(got) != string(original) {
				t.Fatal("candidate published before test")
			}
			return errors.New("validation failed")
		}
		return nil
	}
	if err := a.ApplyCommitted(ctx, "demo", next, old); err == nil {
		t.Fatal("expected validation failure")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("committed state not restored")
	}
	r.hook = nil
	if err := a.ApplyCommitted(ctx, "demo", next, old); err != nil {
		t.Fatal(err)
	}
	// Simulate crash after publish, before SQLite checkpoint.
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != string(original) {
		t.Fatal("replay did not restore committed state")
	}
	// Crash after checkpoint: recovery projects the new state, regardless of disk.
	if err := a.Recover(ctx, "demo", next); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) == string(original) {
		t.Fatal("post-checkpoint recovery stale")
	}
	if err := a.Recover(ctx, "demo", absent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected absent pool: %v", err)
	}
	if _, err := os.Stat(path + ".lkg"); !os.IsNotExist(err) {
		t.Fatal("unexpected marker")
	}
}
func TestPoolRecoveryFailureRetries(t *testing.T) {
	ctx := context.Background()
	r := &poolRunner{}
	a := PoolApplier{Dir: testPoolDir(t), Runner: r, Probe: func(context.Context) error { return nil }}
	old := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}
	next := PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 4}
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
	r.hook = func(ctx context.Context, n int, _ []string) error {
		if n >= 4 {
			return errors.New("reload failure")
		}
		return nil
	}
	if err := a.ApplyCommitted(ctx, "demo", next, old); !errors.Is(err, jobs.ErrRetry) {
		t.Fatalf("expected retry: %v", err)
	}
	r.hook = nil
	if err := a.Recover(ctx, "demo", old); err != nil {
		t.Fatal(err)
	}
}
func TestPoolRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "novapanel-demo.conf")); err != nil {
		t.Fatal(err)
	}
	a := PoolApplier{Dir: dir, Runner: &poolRunner{}, Probe: func(context.Context) error { return nil }}
	if err := a.Recover(context.Background(), "demo", PoolState{SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Exists: true, MaxChildren: 3}); err == nil {
		t.Fatal("accepted symlink")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "safe" {
		t.Fatal("modified outside file")
	}
}

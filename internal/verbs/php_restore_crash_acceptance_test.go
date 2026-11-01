package verbs_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

// The typed runner is the only simulated OS boundary. In the recovery child it
// kills the process at validation, after both snapshots have been restored but
// before validation, reload and probe have completed.
type restoreCrashRunner struct{ kill bool }

func (r restoreCrashRunner) Run(ctx context.Context, name string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if r.kill {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		panic("SIGKILL failed")
	}
	return nil, nil, 0, nil
}

func TestPHPQueueDeathDuringRestore(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("PHP recovery snapshot custody requires root")
	}
	if os.Getenv("NOVAPANEL_PHP_RESTORE_CHILD") == "1" {
		root := os.Getenv("NOVAPANEL_PHP_RESTORE_ROOT")
		if root == "" {
			t.Fatal("missing child scratch root")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		st, log, a := openRestoreCrashFixture(t, root, true)
		defer st.Close()
		defer log.Close()
		if err := a.PrepareStartup(ctx); err != nil {
			t.Fatalf("restore before kill: %v", err)
		}
		t.Fatal("restore did not reach runner")
	}
	root := t.TempDir()
	for _, d := range []string{"sites-available", "sites-enabled", "pool", "journal"} {
		if err := os.Mkdir(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, log, a := openRestoreCrashFixture(t, root, false)
	old := nginx.Desired{Name: "demo", Domains: []string{"demo.example.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", PHP: &php.Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}}
	if err := a.Nginx.ApplyPHP(ctx, a.PHP.Pools, old, func(context.Context) error { return nil }, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	paths := []string{"pool/demo.conf", "pool/demo.conf.lkg", "sites-available/demo.conf", "sites-available/demo.conf.lkg"}
	originals := make([][]byte, len(paths))
	for i, p := range paths {
		var err error
		originals[i], err = os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	version := "8.3"
	if _, err := st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "php", Docroot: "/srv/sites/demo/public", PHPVersion: &version}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(a)
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "admin-session", UserID: "admin-user"}), rpc.Request{Verb: "php.set", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512}}`, sid))})
	if !reply.OK {
		t.Fatalf("dispatch: %+v", reply)
	}
	job := reply.Data.(jobs.EnqueueResult).Job
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_php_terminal BEFORE INSERT ON site_php_limits BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Jobs.Run(ctx); err == nil {
		t.Fatal("expected SQL failure")
	}
	op, err := st.LoadOperation(ctx, job.ID)
	if err != nil || op.Checkpoint != "verified" {
		t.Fatalf("operation=%+v err=%v", op, err)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, `DROP TRIGGER fail_php_terminal`); return err }); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPHPQueueDeathDuringRestore$")
	cmd.Env = append(os.Environ(), "NOVAPANEL_PHP_RESTORE_CHILD=1", "NOVAPANEL_PHP_RESTORE_ROOT="+root)
	out, err := cmd.CombinedOutput()
	var ex *exec.ExitError
	if !errors.As(err, &ex) || ex.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("expected recovery SIGKILL: %v %s", err, out)
	}
	// A clean reopen after the interrupted restore must finish the same job once.
	before, err := os.Stat(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	st, log, a = openRestoreCrashFixture(t, root, false)
	defer st.Close()
	defer log.Close()
	after, err := os.Stat(filepath.Join(root, "panel.db"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("database lock inode changed: %v", err)
	}
	if other, err := store.Open(filepath.Join(root, "panel.db")); err == nil {
		other.Close()
		t.Fatal("lifetime lock not held after recovery reopen")
	}
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	for i, p := range paths {
		got, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || !bytes.Equal(got, originals[i]) {
			t.Fatalf("%s not restored exactly: %v", p, err)
		}
	}
	got, err := st.GetJob(ctx, job.ID)
	if err != nil || got.State != "failed" {
		t.Fatalf("job=%+v err=%v", got, err)
	}
	var id string
	var count int
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*), coalesce(min(id),'') FROM events WHERE verb='php.set' AND json_extract(detail_json,'$.publication')='not_committed'`).Scan(&count, &id)
	})
	if err != nil || count != 1 {
		t.Fatalf("failure audits=%d id=%q err=%v", count, id, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "audit.log"))
	if err != nil || strings.Count(string(data), id) != 1 {
		t.Fatalf("file audit id=%q count=%d err=%v", id, strings.Count(string(data), id), err)
	}
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	var again int
	err = st.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE verb='php.set' AND json_extract(detail_json,'$.publication')='not_committed'`).Scan(&again)
	})
	if err != nil || again != 1 {
		t.Fatalf("replayed audits=%d err=%v", again, err)
	}
}

func openRestoreCrashFixture(t *testing.T, root string, kill bool) (*store.Store, *audit.Log, *agent.Agent) {
	t.Helper()
	st, err := store.Open(filepath.Join(root, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(st, filepath.Join(root, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Store = st
	a.Audit = log
	r := restoreCrashRunner{kill: kill}
	a.Nginx = &nginx.Applier{Root: root, Runner: r, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.PHP = &agent.PHPDependencies{Pools: &php.Applier{Root: filepath.Join(root, "pool"), Runner: r, Probe: func(context.Context, php.Desired) error { return nil }}, RecoveryRoot: filepath.Join(root, "journal"), ResolveIdentity: func(context.Context, store.Site) (uint32, uint32, error) { return 1001, 1001, nil }}
	rpc.NewServer(a).ConfigureJobs()
	return st, log, a
}

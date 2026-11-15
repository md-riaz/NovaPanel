package verbs_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	mainphp "novapanel/internal/php"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

// The typed runner is the only simulated OS boundary. In the recovery child it
// kills the process at validation, after both snapshots have been restored but
// before validation, reload and probe have completed.
type restoreCrashRunner struct{ kill bool }

func (r restoreCrashRunner) Run(ctx context.Context, name string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if r.kill && name == "/usr/sbin/php-fpm8.3" {
		_ = unix.Kill(os.Getpid(), unix.SIGKILL)
		panic("SIGKILL failed")
	}
	return (phpScratchRunner{}).Run(ctx, name, args, opts)
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
	master := "[global]\npid = /run/php/php8.3-fpm.pid\nerror_log = /var/log/php8.3-fpm.log\ninclude = " + filepath.Join(root, "pool", "*.conf") + "\n"
	if err := os.WriteFile(filepath.Join(root, "php-fpm.conf"), []byte(master), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, log, a := openRestoreCrashFixture(t, root, false)
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
	op, err := st.LoadOperation(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := op.Identity.PHP
	limits := store.PHPLimits{MemoryMB: &p.OriginalLimits.MemoryMB, UploadMB: &p.OriginalLimits.UploadMB, MaxExecSec: &p.OriginalLimits.MaxExecSec, MaxChildren: &p.OriginalCap}
	if err := a.PHP.Recover(ctx, "demo", mainphp.PoolState{SiteID: sid, Exists: true, MaxChildren: p.OriginalCap, Limits: limits}); err != nil {
		t.Fatal(err)
	}
	paths := []string{"pool/novapanel-demo.conf"}
	originals := make([][]byte, len(paths))
	for i, p := range paths {
		originals[i], err = os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_php_terminal BEFORE INSERT ON settings WHEN NEW.key LIKE 'site:php_limits:%' BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Jobs.Run(ctx); err == nil {
		t.Fatal("expected SQL failure")
	}
	op, err = st.LoadOperation(ctx, job.ID)
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
	out, stderr, exit, err := (runner.Real{}).Run(ctx, os.Args[0], []string{"-test.run=^TestPHPQueueDeathDuringRestore$"}, runner.RunOpts{Env: append(os.Environ(), "NOVAPANEL_PHP_RESTORE_CHILD=1", "NOVAPANEL_PHP_RESTORE_ROOT="+root)})
	if exit != -1 || err == nil || err.Error() != "signal: killed" || ctx.Err() != nil {
		t.Fatalf("expected recovery SIGKILL: exit=%d err=%v %s %s", exit, err, out, stderr)
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
	a.Exec = r
	a.Nginx = &nginx.Applier{Root: root, Runner: r, Probe: func(context.Context, nginx.Desired) error { return nil }}
	a.PHP = &mainphp.PoolApplier{Dir: filepath.Join(root, "pool"), Runner: r, Probe: func(context.Context) error { return nil }}
	rpc.NewServer(a).ConfigureJobs()
	return st, log, a
}

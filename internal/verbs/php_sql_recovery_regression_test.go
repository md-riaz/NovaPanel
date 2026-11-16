package verbs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

// A terminal SQL failure occurs after the renderer has published and promoted
// both desired files. A fresh startup must restore the original, not replay it.
func TestPHPStartupRestoresAfterTerminalSQLFailure(t *testing.T) {
	requireRootPHPCustody(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
	dbPath, auditPath := filepath.Join(root, "panel.db"), filepath.Join(root, "audit.log")
	open := func() (*store.Store, *audit.Log, *agent.Agent, *rpc.Server) {
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		log, err := audit.Open(st, auditPath)
		if err != nil {
			t.Fatal(err)
		}
		a := agent.New()
		a.Store, a.Audit = st, log
		a.Exec = phpScratchRunner{}
		a.Nginx = &nginx.Applier{Root: root, Runner: phpScratchRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
		a.PHP = &php.PoolApplier{Dir: filepath.Join(root, "pool"), Runner: a.Exec, Probe: func(context.Context) error { return nil }}
		server := rpc.NewServer(a)
		server.ConfigureJobs()
		return st, log, a, server
	}
	st, log, a, server := open()
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
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Kind: "user", Role: "admin", Session: "admin-session", UserID: "admin-user"}), rpc.Request{Verb: "php.set", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"version":"8.3","limits":{"memory_mb":512}}`, sid))})
	if !reply.OK {
		t.Fatalf("dispatch: %+v", reply)
	}
	job := reply.Data.(jobs.EnqueueResult).Job
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_php_terminal BEFORE INSERT ON settings WHEN NEW.key LIKE 'site:php_limits:%' BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Jobs.Run(ctx); err == nil {
		t.Fatal("expected terminal SQL failure")
	}
	op, err := st.LoadOperation(ctx, job.ID)
	if err != nil || op.Checkpoint != "verified" {
		t.Fatalf("checkpoint=%+v err=%v", op, err)
	}
	pool, err := os.ReadFile(filepath.Join(root, "pool", "novapanel-demo.conf"))
	if err != nil || !strings.Contains(string(pool), "memory_limit] = 512M") {
		t.Fatalf("expected desired bytes before recovery: %s %v", pool, err)
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
	st, log, a, _ = open()
	defer st.Close()
	defer log.Close()
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatalf("startup must restore precommit original: %v", err)
	}
	pool, err = os.ReadFile(filepath.Join(root, "pool", "novapanel-demo.conf"))
	if err != nil || strings.Contains(string(pool), "memory_limit] = 512M") {
		t.Fatalf("original pool not restored: %s %v", pool, err)
	}
	got, err := st.GetJob(ctx, job.ID)
	if err != nil || got.State != "failed" {
		t.Fatalf("job=%+v err=%v", got, err)
	}
}

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
	"novapanel/internal/render/nginx"
	"novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

// A terminal SQL failure occurs after the renderer has published and promoted
// both desired files. A fresh startup must restore the original, not replay it.
func TestPHPRecoveryAuditReplayPersistent(t *testing.T) {
	requireRootPHPCustody(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	for _, d := range []string{"sites-available", "sites-enabled", "pool", "journal"} {
		if err := os.Mkdir(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
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
		a.Nginx = &nginx.Applier{Root: root, Runner: phpScratchRunner{}, Probe: func(context.Context, nginx.Desired) error { return nil }}
		a.PHP = &agent.PHPDependencies{Pools: &php.Applier{Root: filepath.Join(root, "pool"), Runner: phpScratchRunner{}, Probe: func(context.Context, php.Desired) error { return nil }}, RecoveryRoot: filepath.Join(root, "journal"), ResolveIdentity: func(context.Context, store.Site) (uint32, uint32, error) { return 1001, 1001, nil }}
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
	for _, invalid := range []string{"", "not-a-ulid"} {
		if got, err := st.PreparePHPFailureAudit(ctx, job.ID, invalid); err != store.ErrSiteInvalid || got != "" {
			t.Fatalf("invalid proposed failure ID %q: id=%q err=%v", invalid, got, err)
		}
	}
	opBefore, err := st.LoadOperation(ctx, job.ID)
	if err != nil || opBefore.Identity.PHP.FailureEventID != "" {
		t.Fatalf("invalid ID changed durable identity: %+v %v", opBefore, err)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_php_terminal BEFORE INSERT ON site_php_limits BEGIN SELECT RAISE(ABORT,'injected'); END`)
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
	pool, err := os.ReadFile(filepath.Join(root, "pool", "demo.conf"))
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
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `CREATE TRIGGER fail_reconcile BEFORE UPDATE OF reconciled ON phase4_operations WHEN NEW.reconciled=1 BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.PrepareStartup(ctx); err == nil {
		t.Fatal("expected reconciliation failure")
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.ExecContext(ctx, `DROP TRIGGER fail_reconcile`); return e }); err != nil {
		t.Fatal(err)
	}
	log.Close()
	st.Close()
	st, log, a, _ = open()
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE verb='php.set' AND outcome='error' AND json_extract(detail_json,'$.job_id')=? AND json_extract(detail_json,'$.stage')='exec' AND json_extract(detail_json,'$.publication')='restored'`, job.ID).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected exactly one terminal failure audit, got %d", n)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var failureID string
	fileCount := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev struct {
			ID, Verb, Outcome string
			Detail            map[string]any
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Verb == "php.set" && ev.Outcome == "error" && ev.Detail["job_id"] == job.ID && ev.Detail["stage"] == "exec" && ev.Detail["publication"] == "restored" {
			fileCount++
			failureID = ev.ID
		}
	}
	if fileCount != 1 {
		t.Fatalf("file terminal failure count=%d", fileCount)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `DELETE FROM events WHERE id=?`, failureID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	log.Close()
	st.Close()
	st, log, a, _ = open()
	defer st.Close()
	defer log.Close()
	if err := a.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE id=?`, failureID).Scan(&n)
	}); err != nil || n != 1 {
		t.Fatalf("mirror replay count=%d err=%v", n, err)
	}
}

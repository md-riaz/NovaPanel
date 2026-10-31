package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	execboundary "novapanel/internal/exec"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/audit"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
	"novapanel/internal/wordpress/configcustody"
)

func TestGeneratedWordPressCredentialsAreIndependent(t *testing.T) {
	seen := map[string]bool{}
	for range 2 {
		c, err := GenerateWordPressCredentials()
		if err != nil {
			t.Fatal(err)
		}
		fields := append([]string{c.DatabasePassword, c.AdminPassword}, c.Salts[:]...)
		for _, v := range fields {
			raw, err := base64.RawURLEncoding.DecodeString(v)
			if err != nil || len(raw) != 32 || seen[v] {
				t.Fatal("invalid or repeated entropy")
			}
			seen[v] = true
		}
	}
}

func TestWordPressConfigQueueCancellationReopen(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned production custody fixture requires root")
	}
	if os.Getenv("NOVAPANEL_CONFIG_JAIL") == "" {
		jail := t.TempDir()
		if err := os.Mkdir(filepath.Join(jail, "scratch"), 0700); err != nil {
			t.Fatal(err)
		}
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(filepath.Join(jail, "proc/sys/kernel/random"), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(jail, "proc/sys/kernel/random/boot_id"), boot, 0400); err != nil {
			t.Fatal(err)
		}
		out, stderr, code, err := (execboundary.Real{}).Run(context.Background(), exe, []string{"-test.run=^TestWordPressConfigQueueCancellationReopen$", "-test.v"}, execboundary.RunOpts{Env: append(os.Environ(), "NOVAPANEL_CONFIG_JAIL="+jail)})
		if err != nil || code != 0 {
			t.Fatalf("confined config test: %s %s %v", out, stderr, err)
		}
		return
	}
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	proc := filepath.Join(os.Getenv("NOVAPANEL_CONFIG_JAIL"), "proc", fmt.Sprint(os.Getpid()))
	if err = os.Mkdir(proc, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(proc, "stat"), stat, 0400); err != nil {
		t.Fatal(err)
	}
	if err := unix.Chroot(os.Getenv("NOVAPANEL_CONFIG_JAIL")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("/"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "/scratch")
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session", Role: "owner", Effectful: true})
	root := t.TempDir()
	sites := filepath.Join(root, "sites")
	home := filepath.Join(sites, "example")
	docroot := filepath.Join(home, "public")
	custody := filepath.Join(root, "custody")
	for _, p := range []string{sites, home, docroot, custody} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{home, docroot} {
		if err := os.Chown(p, 23451, 23452); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(home, 0751); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(docroot, os.ModeSetgid|0750); err != nil {
		t.Fatal(err)
	}
	dbpath := filepath.Join(root, "panel.db")
	s, err := store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	auditSink, err := audit.Open(s, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { auditSink.Close() }()
	q := NewQueue(s, seal, auditSink)
	q.SetConfigCustody(custody)
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	m := wordpress.ApprovedArtifacts()
	r := store.WordPressReservation{Input: wordpress.InstallInput{SitesRoot: sites, SiteName: "example", PrimaryDomain: "example.test", UID: 23451, GID: 23452, Locale: "en_US", Administrator: wordpress.Administrator{Title: "Example", Username: "Admin", Email: "owner@example.test"}}, Evidence: wordpress.ArtifactEvidence{CLISHA256: m.CLI.SHA256, WordPressSHA256: m.WordPress.SHA256, ChecksumsSHA256: m.ChecksumsSHA256, SignatureVerified: true, SignerFingerprint: m.CLISignerFingerprint}}
	c, err := GenerateWordPressCredentials()
	if err != nil {
		t.Fatal(err)
	}
	en, err := q.EnqueueWordPress(ctx, r, c)
	if err != nil {
		t.Fatal(err)
	}
	heldOp, err := s.LoadOperation(ctx, en.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Every persisted key must block actual worker entry, not just appear in a list.
	for _, key := range heldOp.Keys() {
		release := q.locks.lock(key)
		entered := make(chan struct{}, 1)
		finished := make(chan error, 1)
		q.SetRunner(func(ctx context.Context, j *Job) error { entered <- struct{}{}; return context.Canceled })
		lockCtx, stop := context.WithCancel(ctx)
		go func() { finished <- q.execute(lockCtx, en.Job) }()
		select {
		case <-entered:
			t.Fatal("worker escaped persisted lock")
		case <-time.After(10 * time.Millisecond):
		}
		stop()
		release()
		if err := <-finished; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		// StartJob can have completed despite cancellation; restore through canonical recovery.
		if err := s.RecoverJobs(ctx, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}

	// Trigger a real SQL checkpoint failure after actual no-replace publication.
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `CREATE TRIGGER stop_config BEFORE UPDATE OF checkpoint ON phase4_operations WHEN NEW.checkpoint='config_published' BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	isolate := func(ctx context.Context, b configcustody.ConfigBinding, effect func(context.Context) error) error {
		if b.UID != 23451 || b.GID != 23452 || b.Docroot != docroot {
			t.Error("wrong resolved binding")
		}
		return effect(ctx)
	}
	run, cancel := context.WithCancel(ctx)
	published := make(chan struct{})
	done := make(chan error, 1)
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if err := s.CheckpointOperation(ctx, j.ID, "prepared", "db_applied"); err != nil {
			return err
		}
		err := q.CreateWordPressConfig(ctx, j.ID, custody, isolate)
		if err == nil {
			t.Error("checkpoint failure ignored")
		}
		close(published)
		<-ctx.Done()
		return ctx.Err()
	})
	go func() { done <- q.Run(run) }()
	select {
	case <-published:
	case <-time.After(5 * time.Second):
		t.Fatal("publication not reached")
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(docroot, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(first, []byte(c.DatabasePassword)) || bytes.Contains(first, []byte(c.AdminPassword)) {
		t.Fatal("wrong password consumer")
	}
	for _, salt := range c.Salts {
		if !bytes.Contains(first, []byte(salt)) {
			t.Fatal("salt omitted")
		}
	}
	op, err := s.LoadOperation(ctx, en.Job.ID)
	if err != nil || op.Checkpoint != "db_applied" || len(op.Ciphertext) == 0 {
		t.Fatal("lost original recovery", err)
	}
	record, err := os.ReadFile(filepath.Join(custody, en.Job.ID+".sealed"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(record, []byte(c.DatabasePassword)) || bytes.Contains(record, []byte(c.Salts[0])) {
		t.Fatal("plaintext custody")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.ExecContext(ctx, "DROP TRIGGER stop_config"); return e }); err != nil {
		t.Fatal(err)
	}
	if err = auditSink.Close(); err != nil {
		t.Fatal(err)
	}
	auditSink, err = audit.Open(s, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueue(s, seal, auditSink)
	// Force cleanup failure after the real terminal transaction, not effect failure.
	q.SetConfigCustody(filepath.Join(custody, "missing"))
	recovered := make(chan struct{}, 1)
	q.SetRunner(func(ctx context.Context, j *Job) error {
		if j.ID != en.Job.ID {
			t.Error("replacement operation")
		}
		if err := q.CreateWordPressConfig(ctx, j.ID, custody, isolate); err != nil {
			return err
		}
		if err := q.CreateWordPressConfig(ctx, j.ID, custody, isolate); err != nil {
			return err
		}
		recovered <- struct{}{}
		return s.CheckpointOperation(ctx, j.ID, "config_published", "verified")
	})
	run, cancel = context.WithCancel(ctx)
	defer cancel()
	go func() { done <- q.Run(run) }()
	select {
	case <-recovered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery not reached")
	}
	deadline := time.After(5 * time.Second)
	for {
		j, e := s.GetJob(ctx, en.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("not terminal")
		case <-time.After(10 * time.Millisecond):
		}
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("post-commit cleanup failure ignored")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup failure did not stop worker")
	}
	cancel()
	if _, e := os.Stat(filepath.Join(custody, en.Job.ID+".sealed")); e != nil {
		t.Fatal("removed before cleanup retry", e)
	}
	q.SetConfigCustody(custody)
	terminalOp, e := s.LoadOperation(ctx, en.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE sites SET docroot=docroot||'-rebound' WHERE id=?`, terminalOp.SiteID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = q.PrepareOperations(ctx); err == nil {
		t.Fatal("cleanup accepted rebound site")
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE sites SET docroot=? WHERE id=?`, docroot, terminalOp.SiteID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = auditSink.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	auditSink, err = audit.Open(s, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueue(s, seal, auditSink)
	q.SetConfigCustody(custody)
	q.SetRunner(func(context.Context, *Job) error { t.Error("committed job replayed after cleanup failure"); return nil })
	// A committed job alone is insufficient when its audit mirror is absent.
	var saved string
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, `SELECT detail_json FROM events WHERE json_extract(detail_json,'$.job_id')=? AND json_extract(detail_json,'$.stage')='exec'`, en.Job.ID).Scan(&saved); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE events SET detail_json='{}' WHERE json_extract(detail_json,'$.job_id')=? AND json_extract(detail_json,'$.stage')='exec'`, en.Job.ID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = q.PrepareOperations(ctx); err == nil {
		t.Fatal("cleanup without durable audit")
	}
	if _, e := os.Stat(filepath.Join(custody, en.Job.ID+".sealed")); e != nil {
		t.Fatal("record removed without audit", e)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE events SET detail_json=? WHERE detail_json='{}' AND verb='site.create' AND outcome='ok'`, saved)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal("terminal cleanup retry", err)
	}
	second, err := os.ReadFile(filepath.Join(docroot, "wp-config.php"))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("recovery changed config", err)
	}
	op, err = s.LoadOperation(ctx, en.Job.ID)
	if err != nil || op.Checkpoint != "done" || len(op.Ciphertext) != 0 {
		t.Fatal("not atomic terminal", err)
	}
	job, err := s.GetJob(ctx, en.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.JobEvents(ctx, en.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := json.Marshal([]any{job, events})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range append([]string{c.DatabasePassword, c.AdminPassword}, c.Salts[:]...) {
		if bytes.Contains(durable, []byte(v)) {
			t.Fatal("plaintext in job/event/audit")
		}
	}
	claim, err := q.Claim(ctx, en.Job.ID, en.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(claim) != 2 || len(claim["wordpress"].(map[string]any)) != 3 {
		t.Fatal("claim leaked noncanonical fields")
	}
	if _, err = q.Claim(ctx, en.Job.ID, en.ClaimToken); err == nil {
		t.Fatal("claim replay")
	}
	if _, err = os.Stat(filepath.Join(custody, en.Job.ID+".sealed")); !os.IsNotExist(err) {
		t.Fatal("terminal custody record retained", err)
	}
	// Reopen after consumed claim. Terminal cleanup must not rerun effects.
	if err = auditSink.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	auditSink, err = audit.Open(s, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueue(s, seal, auditSink)
	q.SetConfigCustody(custody)
	q.SetRunner(func(context.Context, *Job) error { t.Error("terminal effects replayed"); return nil })
	if err = q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Claim(ctx, en.Job.ID, en.ClaimToken); err == nil {
		t.Fatal("claim recreated")
	}
}

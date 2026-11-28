package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"novapanel/internal/config"
	"novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/id"
)

// Restore failures are usually transient (ufw busy, a short write error), so the timer-run revert
// retries with backoff. The lock stays held across retries, so the attempts are bounded: the daemon's
// firewall RPCs wait on the same lock, and a permanent failure must not block them forever.
var (
	revertAttempts  = 6
	revertFirstWait = time.Second
	revertMaxWait   = 30 * time.Second
)

// firewallRevert runs independently of the daemon's database lifetime lock.
func firewallRevert(ctx context.Context, dbPath, rulesDir, snapshot string, runner exec.Runner) error {
	if !filepath.IsAbs(snapshot) || filepath.Clean(snapshot) != snapshot {
		return errors.New("invalid snapshot path")
	}
	release, err := firewall.Lock(ctx, filepath.Join(filepath.Dir(dbPath), "firewall.lock"))
	if err != nil {
		return err
	}
	defer release()
	return firewall.Retry(ctx, revertAttempts, revertFirstWait, revertMaxWait, func() error {
		return revertPending(ctx, dbPath, rulesDir, snapshot, runner)
	}, func(err error, wait time.Duration) {
		fmt.Fprintf(os.Stderr, "firewall revert failed, retrying in %s: %v\n", wait, err)
	})
}

// revertPending is idempotent: a finished revert finds no pending row and returns nil.
func revertPending(ctx context.Context, dbPath, rulesDir, snapshot string, runner exec.Runner) error {
	dsn := (&url.URL{Scheme: "file", Path: dbPath}).String() + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	var pendingID string
	err = db.QueryRowContext(ctx, "SELECT id FROM fw_pending WHERE snapshot_path=? AND confirmed=0", snapshot).Scan(&pendingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !id.Valid(pendingID) {
		return errors.New("invalid pending id")
	}
	if err = firewall.Restore(ctx, runner, rulesDir, snapshot); err != nil {
		return err
	}
	marker := filepath.Join(filepath.Dir(dbPath), "fw-reverted-"+pendingID)
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(pendingID + "\n")
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(marker))
	if err != nil {
		return err
	}
	err = errors.Join(dir.Sync(), dir.Close())
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE jobs SET step='reverted' WHERE verb='firewall.apply' AND json_extract(result_json,'$.pending_id')=? AND step IN ('armed','applied')`, pendingID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM fw_pending WHERE id=? AND snapshot_path=? AND confirmed=0", pendingID, snapshot)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("pending firewall change changed")
	}
	return tx.Commit()
}

func runFirewallRevert(snapshot string) error {
	if geteuid() != 0 {
		return errors.New("panel-agent must run as root")
	}
	cfg, err := config.Load(config.DefaultPath, nil)
	if err != nil {
		return err
	}
	if err = firewallRevert(context.Background(), cfg.DBPath, "/etc/ufw", snapshot, exec.Real{}); err != nil {
		return fmt.Errorf("firewall revert: %w", err)
	}
	return nil
}

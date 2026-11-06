package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/firewall"
)

// journalSnapshot models restartable rules/state restoration and a durable audit
// marker using scratch files only. It is not an implementation of UFW recovery.
type journalSnapshot struct {
	root string
	fail string
}

func (s *journalSnapshot) RestoreAndMark(_ context.Context, p firewall.Pending) error {
	for _, stage := range []string{"rules-and-enabled", "audit-marker"} {
		if s.fail == stage {
			return errors.New("injected interruption")
		}
		path := filepath.Join(s.root, stage)
		if b, err := os.ReadFile(path); err == nil {
			if string(b) != p.ID {
				return errors.New("marker belongs to another pending ID")
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := f.WriteString(p.ID)
		syncErr := f.Sync()
		if err := errors.Join(writeErr, syncErr, f.Close()); err != nil {
			return err
		}
		dir, err := os.Open(s.root)
		if err != nil {
			return err
		}
		syncErr = dir.Sync()
		if err := errors.Join(syncErr, dir.Close()); err != nil {
			return err
		}
	}
	return nil
}

func TestFirewallRecoveryReplayAcrossPublicationFailures(t *testing.T) {
	for _, stage := range []string{"rules-and-enabled", "audit-marker", "delete", "commit"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "panel.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			firewallFixture(t, s)
			p := pendingFixture()
			if err := s.CreateFirewallPending(ctx, p); err != nil {
				t.Fatal(err)
			}
			ddl := ""
			switch stage {
			case "delete":
				ddl = `CREATE TRIGGER fail_publish BEFORE DELETE ON fw_pending BEGIN SELECT RAISE(ABORT,'injected delete failure'); END;`
			case "commit":
				// DELETE succeeds, but the deferred constraint fails at actual COMMIT.
				ddl = `CREATE TABLE fw_parent(id INTEGER PRIMARY KEY); CREATE TABLE fw_child(id INTEGER REFERENCES fw_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fail_publish AFTER DELETE ON fw_pending BEGIN INSERT INTO fw_child VALUES(1); END;`
			}
			if ddl != "" {
				if err := s.Tx(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(ddl); return err }); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			snap := &journalSnapshot{root: root, fail: stage}
			run := &pendingRunner{}
			r := firewall.Recovery{Store: s, Runner: run, Snapshot: snap, Now: func() time.Time { return p.Deadline }}
			if err := r.Fire(ctx, p.ID); err == nil {
				t.Fatal("publication failure ignored")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WithPending(ctx, p.ID, func(got firewall.Pending) (bool, error) {
				if got != p {
					t.Fatalf("pending changed: %+v", got)
				}
				return false, nil
			}); err != nil {
				t.Fatal("pending lost before durable completion", err)
			}
			if ddl != "" {
				if err := s.Tx(ctx, func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER fail_publish"); return err }); err != nil {
					t.Fatal(err)
				}
			}
			// Reconstruct both dependencies, as after daemon death. Existing markers
			// bind replay to the same ID, including after restoration already finished.
			r.Store = s
			r.Snapshot = &journalSnapshot{root: root}
			if err := r.Fire(ctx, p.ID); err != nil {
				t.Fatal("replay failed", err)
			}
			for _, name := range []string{"rules-and-enabled", "audit-marker"} {
				b, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(b) != p.ID {
					t.Fatalf("missing durable %s: %q %v", name, b, err)
				}
			}
			if err := r.Fire(ctx, p.ID); firewallCode(err) != "not_found" {
				t.Fatal("nonce replay accepted", err)
			}
			if run.calls != 0 {
				t.Fatal("recovery cancelled timer")
			}
		})
	}
}

func TestFirewallIndependentOpenBlockedUntilDaemonStoreCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if other, err := Open(path); err == nil {
		other.Close()
		t.Fatal("expected current lifetime ownership to block independent deadman")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
}

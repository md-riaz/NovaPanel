package audit

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"novapanel/internal/firewall"
	"novapanel/internal/store"
)

type firewallFailSync struct{ Sink }

func (*firewallFailSync) Sync() error { return errors.New("injected audit sync failure") }

func TestFirewallTerminalReconciliation(t *testing.T) {
	for _, terminal := range []string{"confirmed", "reverted", "ack-failure", "commit-failure", "audit-failure"} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			db := filepath.Join(root, "panel.db")
			s, err := store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			p := firewall.Pending{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SnapshotPath: "/not-opened", RevertUnit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: time.Unix(2000, 0)}
			if err = s.CreateFirewallPending(ctx, p); err != nil {
				t.Fatal(err)
			}
			jr := filepath.Join(root, "journal")
			if err = os.Mkdir(jr, 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(jr, "lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			j, err := firewall.OpenJournal(jr)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			r := firewall.JournalRecord{Version: 1, ID: p.ID, Unit: p.RevertUnit, Deadline: 2000, SnapshotSHA256: strings.Repeat("a", 64), State: "armed"}
			if err = j.Arm(ctx, r); err != nil {
				t.Fatal(err)
			}
			if terminal == "confirmed" {
				err = j.Confirm(ctx, p.ID, func() time.Time { return time.Unix(1999, 0) }, func(string) error { return nil })
			} else {
				err = j.Fire(ctx, p.ID, func() time.Time { return p.Deadline }, func(firewall.JournalRecord) error { return nil })
			}
			if err != nil {
				t.Fatal(err)
			}
			logpath := filepath.Join(root, "audit")
			l, err := Open(s, logpath)
			if err != nil {
				t.Fatal(err)
			}
			if terminal == "audit-failure" {
				l.sink = &firewallFailSync{Sink: l.sink}
				if err = l.ReconcileFirewall(ctx, j); err == nil {
					t.Fatal("audit sync failure accepted")
				}
				if l.Healthy() == nil {
					t.Fatal("audit failure did not latch")
				}
				if err = s.WithPending(ctx, p.ID, func(firewall.Pending) (bool, error) { return false, nil }); err != nil {
					t.Fatal("audit failure consumed row", err)
				}
				l.Close()
				s.Close()
				s, err = store.Open(db)
				if err != nil {
					t.Fatal(err)
				}
				l, err = Open(s, logpath)
				if err != nil {
					t.Fatal(err)
				}
			}
			if terminal == "ack-failure" {
				// A directory at the exclusive publication temp name prevents the
				// acknowledgement after SQL has committed, without changing state.
				if err = os.Mkdir(filepath.Join(jr, "state.next"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if terminal == "commit-failure" {
				if err = s.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.Exec(`CREATE TABLE fw_parent(id INTEGER PRIMARY KEY); CREATE TABLE fw_child(id INTEGER REFERENCES fw_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fw_commit_fail AFTER DELETE ON fw_pending BEGIN INSERT INTO fw_child VALUES(1); END;`)
					return e
				}); err != nil {
					t.Fatal(err)
				}
			}
			if terminal == "ack-failure" || terminal == "commit-failure" {
				if err = l.ReconcileFirewall(ctx, j); err == nil {
					t.Fatal("fault did not fail")
				}
				if terminal == "ack-failure" {
					if err = os.Remove(filepath.Join(jr, "state.next")); err != nil {
						t.Fatal(err)
					}
				} else {
					if err = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DROP TRIGGER fw_commit_fail"); return e }); err != nil {
						t.Fatal(err)
					}
				}
				l.Close()
				s.Close()
				s, err = store.Open(db)
				if err != nil {
					t.Fatal(err)
				}
				// Ordinary audit replay can mirror the file-only event before the
				// pending row is consumed. Reconciliation must still delete it.
				l, err = Open(s, logpath)
				if err != nil {
					t.Fatal(err)
				}
				fresh, e := firewall.OpenJournal(jr)
				if e != nil {
					t.Fatal(e)
				}
				defer fresh.Close()
				j = fresh
			}
			if err = l.ReconcileFirewall(ctx, j); err != nil {
				t.Fatal(err)
			}
			if err = l.ReconcileFirewall(ctx, j); err != nil {
				t.Fatal(err)
			}
			events, err := s.ListEvents(ctx, 10, "")
			if err != nil || len(events) != 1 || events[0].ID != p.ID {
				t.Fatalf("events %+v %v", events, err)
			}
			if err = s.WithPending(ctx, p.ID, func(firewall.Pending) (bool, error) { t.Fatal("pending survived"); return false, nil }); err == nil {
				t.Fatal("missing pending accepted")
			}
			if other, e := store.Open(db); e == nil {
				other.Close()
				t.Fatal("lifetime flock weakened")
			}
			l.Close()
			s.Close()
			s, err = store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			l, err = Open(s, logpath)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			if err = l.ReconcileFirewall(ctx, j); err != nil {
				t.Fatal(err)
			}
		})
	}
}

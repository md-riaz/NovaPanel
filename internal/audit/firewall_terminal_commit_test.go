package audit

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"novapanel/internal/firewall"
	"novapanel/internal/store"
)

func TestFirewallTerminalCommitReplay(t *testing.T) {
	for _, state := range []string{"confirmed", "reverted"} {
		for _, fault := range []string{"none", "sync", "commit", "delete"} {
			t.Run(state+"/"+fault, func(t *testing.T) {
				root := t.TempDir()
				ctx := context.Background()
				s, e := store.Open(root + "/panel.db")
				if e != nil {
					t.Fatal(e)
				}
				defer func() { s.Close() }()
				must := func(e error) {
					t.Helper()
					if e != nil {
						t.Fatal(e)
					}
				}
				r := firewall.JournalRecord{Version: 1, ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Unit: "novapanel-fwrevert-01ARZ3NDEKTSV4RRFFQ69G5FAV", Deadline: 100, SnapshotSHA256: strings.Repeat("a", 64), State: "armed"}
				must(s.CreateFirewallPending(ctx, firewall.Pending{ID: r.ID, RevertUnit: r.Unit, Deadline: time.Unix(100, 0), SnapshotPath: "/ignored"}))
				must(os.Mkdir(root+"/journal", 0700))
				must(os.WriteFile(root+"/journal/lock", nil, 0600))
				j, e := firewall.OpenJournal(root + "/journal")
				must(e)
				defer j.Close()
				must(j.Arm(ctx, r))
				if state == "confirmed" {
					must(j.Confirm(ctx, r.ID, func() time.Time { return time.Unix(90, 0) }, func(string) error { return nil }))
				} else {
					must(j.Fire(ctx, r.ID, func() time.Time { return time.Unix(100, 0) }, func(firewall.JournalRecord) error { return nil }))
				}
				r.State = state
				l, e := Open(s, root+"/audit")
				must(e)
				defer func() { l.Close() }()
				switch fault {
				case "sync":
					l.sink = &firewallFailSync{Sink: l.sink}
				case "commit":
					must(s.Tx(ctx, func(tx *sql.Tx) error {
						_, e := tx.Exec(`CREATE TABLE terminal_parent(id INTEGER PRIMARY KEY);CREATE TABLE terminal_child(id INTEGER REFERENCES terminal_parent(id) DEFERRABLE INITIALLY DEFERRED);CREATE TRIGGER terminal_failure AFTER DELETE ON fw_pending BEGIN INSERT INTO terminal_child VALUES(1);END;`)
						return e
					}))
				case "delete":
					must(s.Tx(ctx, func(tx *sql.Tx) error {
						_, e := tx.Exec(`CREATE TRIGGER terminal_failure BEFORE DELETE ON fw_pending BEGIN SELECT RAISE(ABORT,'delete fault');END;`)
						return e
					}))
				}
				if fault != "none" {
					if l.CommitFirewallTerminal(ctx, j) == nil {
						t.Fatal("fault accepted")
					}
					must(s.WithPending(ctx, r.ID, func(firewall.Pending) (bool, error) { return false, nil }))
					if fault == "commit" || fault == "delete" {
						must(s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DROP TRIGGER terminal_failure"); return e }))
					}
					must(l.Close())
					must(s.Close())
					s, e = store.Open(root + "/panel.db")
					must(e)
					l, e = Open(s, root+"/audit")
					must(e)
					must(s.WithPending(ctx, r.ID, func(firewall.Pending) (bool, error) { return false, nil }))
				}
				must(l.CommitFirewallTerminal(ctx, j))
				must(l.CommitFirewallTerminal(ctx, j))
				got, p, e := j.Inspect(ctx)
				must(e)
				if !p || got != r {
					t.Fatal("terminal changed", got)
				}
				events, e := s.ListEvents(ctx, 10, "")
				must(e)
				if len(events) != 1 || events[0].ID != r.ID {
					t.Fatal(events)
				}
			})
		}
	}
}

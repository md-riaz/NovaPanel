package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/firewall"
)

func terminalFixture() firewall.JournalRecord {
	p := pendingFixture()
	return firewall.JournalRecord{Version: 1, ID: p.ID, Unit: p.RevertUnit, Deadline: p.Deadline.Unix(), SnapshotSHA256: strings.Repeat("a", 64), State: "reverted"}
}
func TestFirewallTerminalCommitFailureRollbackAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	firewallFixture(t, s)
	p := pendingFixture()
	if e = s.CreateFirewallPending(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`CREATE TABLE fw_parent(id INTEGER PRIMARY KEY); CREATE TABLE fw_child(id INTEGER REFERENCES fw_parent(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER fw_commit_fail AFTER DELETE ON fw_pending BEGIN INSERT INTO fw_child VALUES(1); END;`)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	appendFile := func(Event) error { calls++; return nil }
	if e = s.ReconcileFirewallTerminal(ctx, terminalFixture(), appendFile); e == nil {
		t.Fatal("commit succeeded")
	}
	// Must work on the same single pooled connection before Close, not only reopen.
	if e = s.WithPending(ctx, p.ID, func(firewall.Pending) (bool, error) { return false, nil }); e != nil {
		t.Fatal("failed COMMIT leaked transaction", e)
	}
	events, e := s.ListEvents(ctx, 10, "")
	if e != nil || len(events) != 0 {
		t.Fatal("rolled-back event leaked", events, e)
	}
	if e = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("DROP TRIGGER fw_commit_fail"); return e }); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileFirewallTerminal(ctx, terminalFixture(), appendFile); e != nil {
		t.Fatal(e)
	}
	if e = s.ReconcileFirewallTerminal(ctx, terminalFixture(), appendFile); e != nil {
		t.Fatal(e)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	events, e = s.ListEvents(ctx, 10, "")
	if e != nil || len(events) != 1 {
		t.Fatal(events, e)
	}
}

func TestFirewallTerminalRejectsMismatches(t *testing.T) {
	for _, kind := range []string{"id", "unit", "deadline", "confirmed", "missing", "outcome", "digest", "collision", "audit", "delete"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s, e := Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			firewallFixture(t, s)
			p := pendingFixture()
			r := terminalFixture()
			if kind != "missing" {
				if e = s.CreateFirewallPending(ctx, p); e != nil {
					t.Fatal(e)
				}
			}
			switch kind {
			case "id":
				r.ID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
				r.Unit = "novapanel-fwrevert-" + r.ID
			case "unit":
				r.Unit = "bad"
			case "deadline":
				r.Deadline++
			case "outcome":
				r.State = "armed"
			case "digest":
				r.SnapshotSHA256 = "bad"
			case "collision":
				e = s.InsertEvent(ctx, Event{ID: p.ID, TS: 1, Actor: "wrong", Verb: "ping", Outcome: "ok", Detail: json.RawMessage(`{}`)})
			case "confirmed":
				e = s.Tx(ctx, func(tx *sql.Tx) error { _, e := tx.Exec("UPDATE fw_pending SET confirmed=1"); return e })
			case "delete":
				e = s.Tx(ctx, func(tx *sql.Tx) error {
					_, e := tx.Exec(`CREATE TRIGGER fw_deny BEFORE DELETE ON fw_pending BEGIN SELECT RAISE(ABORT,'deny'); END`)
					return e
				})
			}
			if e != nil {
				t.Fatal(e)
			}
			calls := 0
			e = s.ReconcileFirewallTerminal(ctx, r, func(Event) error {
				calls++
				if kind == "audit" {
					return errors.New("sink failed")
				}
				return nil
			})
			if e == nil {
				t.Fatal("unsafe reconciliation succeeded")
			}
			if kind != "audit" && kind != "delete" && calls != 0 {
				t.Fatal("audit before validation")
			}
			var count int
			if e = s.db.QueryRowContext(ctx, "SELECT count(*) FROM fw_pending").Scan(&count); e != nil {
				t.Fatal(e)
			}
			if kind != "missing" && count != 1 {
				t.Fatal("row consumed")
			}
		})
	}
}

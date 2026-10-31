package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"novapanel/internal/audit"
	"novapanel/internal/firewall"
	"novapanel/internal/store"
)

func terminalSQL(t *testing.T, a *Agent, query string, args ...any) {
	t.Helper()
	mustTerminal(t, a.Store.Tx(t.Context(), func(tx *sql.Tx) error { _, e := tx.Exec(query, args...); return e }))
}
func terminalCounts(t *testing.T, a *Agent, pending, events int) {
	t.Helper()
	mustTerminal(t, a.Store.Tx(t.Context(), func(tx *sql.Tx) error {
		for q, w := range map[string]int{"SELECT count(*) FROM fw_pending": pending, "SELECT count(*) FROM events WHERE actor='firewall-recovery'": events} {
			var n int
			if e := tx.QueryRow(q).Scan(&n); e != nil {
				return e
			}
			if n != w {
				t.Fatalf("%s: %d want %d", q, n, w)
			}
		}
		return nil
	}))
}
func terminalEvent(r firewall.JournalRecord) store.Event {
	detail, _ := json.Marshal(r)
	verb := "firewall.revert"
	if r.State == "confirmed" {
		verb = "firewall.confirm"
	}
	return store.Event{ID: r.ID, TS: r.Deadline * 1000, Actor: "firewall-recovery", Verb: verb, ObjectKey: "firewall:" + r.ID, Outcome: "ok", Detail: detail}
}

func TestFirewallTerminalProjectionMatrix(t *testing.T) {
	for _, terminal := range []string{"confirmed", "reverted"} {
		t.Run(terminal, func(t *testing.T) { firewallTerminalProjectionMatrix(t, terminal) })
	}
}
func firewallTerminalProjectionMatrix(t *testing.T, terminal string) {
	for _, fault := range []string{"exact", "second", "id", "unit", "deadline", "confirmed", "missing", "missing-evidence", "present-evidence", "ts", "actor", "role", "verb", "object", "outcome", "code", "detail"} {
		t.Run(fault, func(t *testing.T) {
			a, j, r, close := terminalAgent(t, true, terminal)
			defer close()
			terminalSQL(t, a, `INSERT INTO repos(id,name,kind,location,created)VALUES('repo','repo','local','/scratch',1);INSERT INTO backups(id,repo_id,target_kind,target_ref,state,created)VALUES('backup','repo','site','site','done',1);INSERT INTO webhook_deliveries VALUES('hook',1)`)
			expected := false
			switch fault {
			case "exact":
				expected = true
			case "second":
				terminalSQL(t, a, "INSERT INTO fw_pending VALUES('foreign','/ignored','foreign',100000,0)")
			case "id":
				terminalSQL(t, a, "UPDATE fw_pending SET id='foreign'")
			case "unit":
				terminalSQL(t, a, "UPDATE fw_pending SET revert_unit='wrong'")
			case "deadline":
				terminalSQL(t, a, "UPDATE fw_pending SET deadline=100001")
			case "confirmed":
				terminalSQL(t, a, "UPDATE fw_pending SET confirmed=1")
			case "missing":
				terminalSQL(t, a, "DELETE FROM fw_pending")
			default:
				ev := terminalEvent(r)
				switch fault {
				case "missing-evidence":
					terminalSQL(t, a, "DELETE FROM fw_pending")
					expected = true
				case "present-evidence":
					expected = true
				case "ts":
					ev.TS++
				case "actor":
					ev.Actor = "other"
				case "role":
					ev.Role = "owner"
				case "verb":
					ev.Verb = "firewall.confirm"
					if r.State == "confirmed" {
						ev.Verb = "firewall.revert"
					}
				case "object":
					ev.ObjectKey = "other"
				case "outcome":
					ev.Outcome = "error"
				case "code":
					ev.Code = "error"
				case "detail":
					ev.Detail = []byte(`{}`)
				}
				mustTerminal(t, a.Store.InsertEvent(t.Context(), ev))
			}
			e := a.CommitFirewallTerminal(t.Context())
			if (e == nil) != expected {
				t.Fatalf("expected success %t: %v", expected, e)
			}
			got, p, e := j.Inspect(t.Context())
			mustTerminal(t, e)
			if !p || got != r {
				t.Fatal("terminal changed", got)
			}
			terminalSQL(t, a, "UPDATE repos SET name=name;UPDATE backups SET state=state;UPDATE webhook_deliveries SET seen_at=seen_at")
			mustTerminal(t, a.Store.Tx(t.Context(), func(tx *sql.Tx) error {
				for _, table := range []string{"repos", "backups", "webhook_deliveries"} {
					var n int
					if e := tx.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
						return e
					}
					if n != 1 {
						t.Fatal("unrelated row lost", table)
					}
				}
				return nil
			}))
			if expected {
				terminalCounts(t, a, 0, 1)
				mustTerminal(t, a.CommitFirewallTerminal(t.Context()))
			}
		})
	}
}

func TestFirewallTerminalFailureReplay(t *testing.T) {
	for _, terminal := range []string{"confirmed", "reverted"} {
		t.Run(terminal, func(t *testing.T) { firewallTerminalFailureReplay(t, terminal) })
	}
}
func firewallTerminalFailureReplay(t *testing.T, terminal string) {
	for _, fault := range []string{"delete", "commit"} {
		t.Run(fault, func(t *testing.T) {
			a, j, r, close := terminalAgent(t, true, terminal)
			defer close()
			ddl := `CREATE TRIGGER terminal_fail BEFORE DELETE ON fw_pending BEGIN SELECT RAISE(ABORT,'delete failure');END;`
			if fault == "commit" {
				ddl = `CREATE TABLE terminal_parent(id INTEGER PRIMARY KEY);CREATE TABLE terminal_child(id INTEGER REFERENCES terminal_parent(id) DEFERRABLE INITIALLY DEFERRED);CREATE TRIGGER terminal_fail AFTER DELETE ON fw_pending BEGIN INSERT INTO terminal_child VALUES(1);END;`
			}
			terminalSQL(t, a, ddl)
			if e := a.CommitFirewallTerminal(t.Context()); e == nil {
				t.Fatal("fault accepted")
			}
			terminalCounts(t, a, 1, 0) // Same pooled connection, before reopen, must be rolled back.
			terminalSQL(t, a, "DROP TRIGGER terminal_fail")
			mustTerminal(t, a.Audit.Close())
			mustTerminal(t, a.Store.Close())
			cleanup, e := a.OpenState(t.Context())
			mustTerminal(t, e)
			defer cleanup()
			terminalCounts(t, a, 1, 1) // Actual OpenState ordinary file replay, row still retained.
			mustTerminal(t, a.CommitFirewallTerminal(t.Context()))
			terminalCounts(t, a, 0, 1)
			got, p, e := j.Inspect(t.Context())
			mustTerminal(t, e)
			if !p || got != r {
				t.Fatal("lost terminal evidence", got)
			}
		})
	}
}

type terminalSink struct {
	audit.Sink
	fault  string
	cancel context.CancelFunc
}

func (s *terminalSink) Write(b []byte) (int, error) {
	if s.fault == "short" {
		return len(b) - 1, nil
	}
	return s.Sink.Write(b)
}
func (s *terminalSink) Sync() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.fault == "sync" {
		return errors.New("sink sync failure")
	}
	return s.Sink.Sync()
}
func TestFirewallTerminalSinkFailures(t *testing.T) {
	for _, terminal := range []string{"confirmed", "reverted"} {
		t.Run(terminal, func(t *testing.T) { firewallTerminalSinkFailures(t, terminal) })
	}
}
func firewallTerminalSinkFailures(t *testing.T, terminal string) {
	for _, fault := range []string{"short", "sync", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			a, j, r, close := terminalAgent(t, true, terminal)
			defer close()
			mustTerminal(t, a.Audit.Close())
			f, e := os.OpenFile(a.Cfg.LogDir+"/audit.log", os.O_APPEND|os.O_WRONLY, 0600)
			mustTerminal(t, e)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sink := &terminalSink{Sink: f, fault: fault}
			if fault == "cancel" {
				sink.cancel = cancel
			}
			a.Audit = audit.New(a.Store, sink)
			e = a.CommitFirewallTerminal(ctx)
			if e == nil {
				t.Fatal("fault accepted")
			}
			if fault == "short" && !errors.Is(e, io.ErrShortWrite) {
				t.Fatal(e)
			}
			terminalCounts(t, a, 1, 0)
			got, p, e := j.Inspect(t.Context())
			mustTerminal(t, e)
			if !p || got != r {
				t.Fatal(got)
			}
			if fault != "cancel" {
				if a.Audit.Healthy() == nil {
					t.Fatal("sink fault not sticky")
				}
				sink.fault = ""
				if a.CommitFirewallTerminal(t.Context()) == nil {
					t.Fatal("sticky log recovered without reopen")
				}
			}
			mustTerminal(t, a.Audit.Close())
			mustTerminal(t, a.Store.Close())
			cleanup, e := a.OpenState(t.Context())
			mustTerminal(t, e)
			defer cleanup()
			mustTerminal(t, a.CommitFirewallTerminal(t.Context()))
			terminalCounts(t, a, 0, 1)
		})
	}
}

func TestFirewallTerminalMissingSchema(t *testing.T) {
	a, j, r, close := terminalAgent(t, false)
	defer close()
	if a.CommitFirewallTerminal(t.Context()) == nil {
		t.Fatal("missing canonical schema accepted")
	}
	got, p, e := j.Inspect(t.Context())
	mustTerminal(t, e)
	if !p || got != r {
		t.Fatal(got)
	}
	mustTerminal(t, a.Store.Tx(t.Context(), func(tx *sql.Tx) error {
		var n int
		e := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('repos','backups','webhook_deliveries','fw_pending')").Scan(&n)
		if n != 0 {
			t.Fatal("fallback migration", n)
		}
		return e
	}))
}
func TestFirewallTerminalInvalidAgent(t *testing.T) {
	for _, a := range []*Agent{nil, {}, New()} {
		if a.CommitFirewallTerminal(t.Context()) == nil {
			t.Fatal("uninitialized accepted")
		}
	}
}

package store

import (
	"database/sql"
	"novapanel/internal/firewall"
	"testing"
)

func TestFirewallReceiptVerification(t *testing.T) {
	for _, outcome := range []string{"confirmed", "reverted"} {
		for _, kind := range []string{"exact", "missing", "pending", "collision", "metadata", "non-normalized"} {
			t.Run(outcome+"/"+kind, func(t *testing.T) {
				s, e := Open(":memory:")
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				r := terminalFixture()
				r.State = outcome
				if kind != "missing" {
					if e = s.CreateFirewallPending(t.Context(), pendingFixture()); e != nil {
						t.Fatal(e)
					}
					if e = s.ReconcileFirewallTerminal(t.Context(), r, func(Event) error { return nil }); e != nil {
						t.Fatal(e)
					}
				}
				if kind == "pending" {
					if e = s.CreateFirewallPending(t.Context(), pendingFixture()); e != nil {
						t.Fatal(e)
					}
				}
				if kind == "collision" {
					e = s.Tx(t.Context(), func(tx *sql.Tx) error { _, e := tx.Exec("UPDATE events SET actor='collision'"); return e })
					if e != nil {
						t.Fatal(e)
					}
				}
				if kind == "metadata" {
					r.PriorEnabled = !r.PriorEnabled
				}
				if kind == "non-normalized" {
					r.TerminalState = outcome
				}
				calls := 0
				e = s.VerifyFirewallTerminalReceipt(t.Context(), r, func(Event) error { calls++; return nil })
				if kind == "exact" {
					if e != nil || calls != 1 {
						t.Fatal(e, calls)
					}
				} else if e == nil || calls != 0 {
					t.Fatal("invalid receipt accepted or appended", e, calls)
				}
				var n int
				if e = s.db.QueryRow("SELECT count(*) FROM fw_pending").Scan(&n); e != nil {
					t.Fatal(e)
				}
				want := 0
				if kind == "pending" {
					want = 1
				}
				if n != want {
					t.Fatal("verification consumed projection", n)
				}
			})
		}
	}
}
func TestFirewallReceiptAbsenceGuard(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.VerifyFirewallAbsent(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = s.CreateFirewallPending(t.Context(), pendingFixture()); e != nil {
		t.Fatal(e)
	}
	if e = s.VerifyFirewallAbsent(t.Context()); e == nil {
		t.Fatal("orphan pending admitted")
	}
}

var _ firewall.JournalRecord

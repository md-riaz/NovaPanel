package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestJournalReceiptCompatibility(t *testing.T) {
	j, r := journalFixture(t)
	defer j.Close()
	r.State = "confirmed"
	mustCustody(t, j.publish(r))
	original, _ := json.Marshal(r)
	if strings.Contains(string(original), "terminal_state") {
		t.Fatal("v1 bytes changed")
	}
	receipt := strings.Replace(string(original), `"version":1`, `"version":2`, 1)
	receipt = strings.Replace(receipt, `"state":"confirmed"`, `"state":"acknowledged","terminal_state":"confirmed"`, 1)
	mustCustody(t, os.WriteFile(j.root+"/state", []byte(receipt), 0600))
	got, p, e := j.Inspect(context.Background())
	if e != nil || !p {
		t.Fatalf("v2 receipt refused: %v", e)
	}
	b, _ := json.Marshal(got)
	if string(b) != receipt {
		t.Fatal(string(b))
	}
	for _, bad := range []string{receipt + "\n", strings.Replace(receipt, `"version":2`, `"version":1`, 1), strings.Replace(receipt, `"terminal_state":"confirmed"`, `"terminal_state":"armed"`, 1), strings.Replace(receipt, `,"terminal_state":"confirmed"`, "", 1), strings.Replace(receipt, `"version":2`, `"version":2,"version":2`, 1)} {
		mustCustody(t, os.WriteFile(j.root+"/state", []byte(bad), 0600))
		if _, _, e = j.Inspect(t.Context()); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestJournalReceiptPublication(t *testing.T) {
	for _, outcome := range []string{"confirmed", "reverted"} {
		for _, fault := range []string{"commit", "cancel", "sync", "none"} {
			t.Run(outcome+"/"+fault, func(t *testing.T) {
				j, r := journalFixture(t)
				defer j.Close()
				r.State = outcome
				mustCustody(t, j.publish(r))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				syncs := 0
				boom := errors.New("receipt boundary")
				j.syncDirectory = func() error {
					syncs++
					if fault == "sync" && syncs == 2 {
						return boom
					}
					return j.dir.Sync()
				}
				commits, verifies := 0, 0
				commit := func(got JournalRecord) error {
					commits++
					if got != r || syncs != 1 {
						t.Fatal("commit before terminal durability", got, syncs)
					}
					if fault == "commit" {
						return boom
					}
					if fault == "cancel" {
						cancel()
					}
					return nil
				}
				verify := func(got JournalRecord) error {
					verifies++
					if got != r {
						t.Fatal("normalization changed bytes", got)
					}
					return nil
				}
				e := j.Reconcile(ctx, commit, verify)
				got, err := j.read()
				mustCustody(t, err)
				switch fault {
				case "none":
					mustCustody(t, e)
				case "sync":
					if !errors.Is(e, ErrAcknowledgementCommitted) || !errors.Is(e, boom) {
						t.Fatal(e)
					}
				case "cancel":
					if !errors.Is(e, context.Canceled) {
						t.Fatal(e)
					}
				case "commit":
					if !errors.Is(e, boom) {
						t.Fatal(e)
					}
				}
				published := fault == "none" || fault == "sync"
				if published {
					if got.Version != 2 || got.TerminalState != outcome || got.State != "acknowledged" {
						t.Fatal(got)
					}
					normalized, e := got.OriginalTerminal()
					mustCustody(t, e)
					if normalized != r {
						t.Fatal(normalized)
					}
				} else if got != r {
					t.Fatal("precommit failure changed outcome", got)
				}
				if commits != 1 {
					t.Fatal(commits)
				}
				j.syncDirectory = nil
				if published {
					mustCustody(t, j.Reconcile(t.Context(), func(JournalRecord) error { t.Fatal("receipt consumed"); return nil }, verify))
					if verifies != 1 {
						t.Fatal(verifies)
					}
					if e = j.Arm(t.Context(), r); e == nil {
						t.Fatal("reuse enabled")
					}
					mustCustody(t, j.Fire(t.Context(), r.ID, func() time.Time { return time.Unix(1000, 0) }, func(JournalRecord) error { t.Fatal("late helper restored"); return nil }))
				}
			})
		}
	}
}

func TestRuntimeStartupPlanRecheck(t *testing.T) {
	for _, kind := range []string{"absent", "terminal", "outcome", "root", "lock", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			l, r, _ := runtimeFixture(t, true)
			j, e := OpenJournal(l.root + "/journal")
			mustCustody(t, e)
			if kind == "absent" {
				mustCustody(t, os.Remove(l.root+"/journal/state"))
			} else {
				r.State = "confirmed"
				if kind == "legacy" {
					r.State = "acknowledged"
				}
				mustCustody(t, j.publish(r))
			}
			p, e := prepareFirewallStartup(t.Context(), l, runtimeManager{})
			if kind == "legacy" {
				if !errors.Is(e, ErrLegacyAcknowledgement) {
					t.Fatal(e)
				}
				j.Close()
				return
			}
			mustCustody(t, e)
			defer p.Close()
			defer j.Close()
			switch kind {
			case "absent":
				r.State = "confirmed"
				mustCustody(t, j.publish(r))
			case "outcome":
				r.State = "reverted"
				mustCustody(t, j.publish(r))
			case "root":
				mustCustody(t, os.Rename(l.root+"/journal", l.root+"/old"))
			case "lock":
				mustCustody(t, os.Rename(l.root+"/journal/lock", l.root+"/journal/oldlock"))
				mustCustody(t, os.WriteFile(l.root+"/journal/lock", nil, 0600))
			}
			calls := 0
			e = p.Reconcile(t.Context(), func(JournalRecord) error { calls++; return nil }, func(JournalRecord) error { calls++; return nil }, func() error { calls++; return nil })
			if kind == "terminal" {
				mustCustody(t, e)
				if calls != 1 {
					t.Fatal(calls)
				}
			} else if e == nil || calls != 0 {
				t.Fatal("changed classification accepted", e, calls)
			}
		})
	}
}

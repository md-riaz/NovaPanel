package audit

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"novapanel/internal/firewall"
	"novapanel/internal/store"
)

// ReconcileFirewall commits terminal journal evidence and consumes its exact
// SQLite projection. This internal adapter has no CLI/RPC/startup wiring.
func (l *Log) ReconcileFirewall(ctx context.Context, j *firewall.Journal) error {
	return l.firewallTerminal(ctx, j, false)
}

// CommitFirewallTerminal commits through the ordinary audit sink and exact SQL
// transaction, retaining terminal journal evidence without acknowledgement.
func (l *Log) CommitFirewallTerminal(ctx context.Context, j *firewall.Journal) error {
	return l.firewallTerminal(ctx, j, true)
}

func (l *Log) firewallTerminal(ctx context.Context, j *firewall.Journal, retain bool) error {
	if l == nil || j == nil {
		return errors.New("firewall reconciliation unavailable")
	}
	// Lock order is audit Log, journal, Store. No code under the Store transaction
	// may reenter Log or Journal. Ordinary Record also takes Log before Store.
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	operation := func(ctx context.Context, commit func(firewall.JournalRecord) error) error {
		return j.Reconcile(ctx, commit, func(r firewall.JournalRecord) error {
			return l.s.VerifyFirewallTerminalReceipt(ctx, r, l.appendFirewallDurable)
		})
	}
	if retain {
		operation = j.CommitTerminal
	}
	return operation(ctx, func(r firewall.JournalRecord) error {
		return l.s.ReconcileFirewallTerminal(ctx, r, l.appendFirewallDurable)
	})
}

// CompleteFirewallStartup owns Audit before the retained journal and exact Store
// transaction. It runs only after ordinary OpenState replay.
func (l *Log) CompleteFirewallStartup(ctx context.Context, p *firewall.FirewallStartupPlan) error {
	if l == nil || p == nil {
		return errors.New("firewall startup unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	return p.Reconcile(ctx, func(r firewall.JournalRecord) error {
		return l.s.ReconcileFirewallTerminal(ctx, r, l.appendFirewallDurable)
	}, func(r firewall.JournalRecord) error {
		return l.s.VerifyFirewallTerminalReceipt(ctx, r, l.appendFirewallDurable)
	}, func() error { return l.s.VerifyFirewallAbsent(ctx) })
}

func (l *Log) appendFirewallDurable(v store.Event) error {
	b, err := json.Marshal(map[string]any{"id": v.ID, "ts": v.TS, "actor": v.Actor, "role": v.Role, "verb": v.Verb, "object_key": v.ObjectKey, "outcome": v.Outcome, "code": v.Code, "detail": v.Detail})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	n, err := l.sink.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = l.sink.Sync()
	}
	if err != nil {
		l.err = err
	}
	return err
}

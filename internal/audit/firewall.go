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
	return j.Reconcile(ctx, func(r firewall.JournalRecord) error {
		return l.s.ReconcileFirewallTerminal(ctx, r, func(v store.Event) error {
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
		})
	})
}

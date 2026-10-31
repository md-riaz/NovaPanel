package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"novapanel/internal/firewall"
	"novapanel/internal/id"
)

// ReconcileFirewallTerminal is daemon-only. appendDurable must append and sync
// the supplied event to the independent audit file without calling Store again.
// A file-only event on SQL failure is reconciliation intent, not SQL completion.
// The caller must hold the journal lock until this returns and acknowledge only
// after success. Production fw_pending migration and public wiring remain gated.
func (s *Store) ReconcileFirewallTerminal(ctx context.Context, r firewall.JournalRecord, appendDurable func(Event) error) error {
	digest, err := hex.DecodeString(r.SnapshotSHA256)
	if appendDurable == nil || r.Version != 1 || !id.Valid(r.ID) || r.Unit != "novapanel-fwrevert-"+r.ID || r.Deadline <= 0 || r.Deadline > 253402300799 || err != nil || len(digest) != 32 || strings.ToLower(r.SnapshotSHA256) != r.SnapshotSHA256 || (r.State != "confirmed" && r.State != "reverted") {
		return errors.New("invalid terminal firewall reconciliation")
	}
	detail, err := json.Marshal(r)
	if err != nil {
		return err
	}
	verb := "firewall.revert"
	if r.State == "confirmed" {
		verb = "firewall.confirm"
	}
	want := Event{ID: r.ID, TS: r.Deadline * 1000, Actor: "firewall-recovery", Verb: verb, ObjectKey: "firewall:" + r.ID, Outcome: "ok", Detail: detail}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE fw_pending SET confirmed=confirmed"); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM fw_pending").Scan(&count); err != nil {
			return err
		}
		var unit string
		var deadline int64
		var confirmed int
		e := tx.QueryRowContext(ctx, "SELECT revert_unit,deadline,confirmed FROM fw_pending WHERE id=?", r.ID).Scan(&unit, &deadline, &confirmed)
		present := e == nil
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if (present && (count != 1 || unit != r.Unit || deadline != want.TS || confirmed != 0)) || (!present && count != 0) {
			return errors.New("firewall projection mismatch")
		}
		var got Event
		var raw []byte
		e = tx.QueryRowContext(ctx, "SELECT id,ts,actor,coalesce(role,''),verb,coalesce(object_key,''),outcome,coalesce(code,''),detail_json FROM events WHERE id=?", r.ID).Scan(&got.ID, &got.TS, &got.Actor, &got.Role, &got.Verb, &got.ObjectKey, &got.Outcome, &got.Code, &raw)
		exists := e == nil
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if exists && (got.ID != want.ID || got.TS != want.TS || got.Actor != want.Actor || got.Role != want.Role || got.Verb != want.Verb || got.ObjectKey != want.ObjectKey || got.Outcome != want.Outcome || got.Code != want.Code || string(raw) != string(want.Detail)) {
			return errors.New("firewall reconciliation event collision")
		}
		if !present && !exists {
			return errors.New("firewall projection missing without reconciliation evidence")
		}
		if err := appendDurable(want); err != nil {
			return err
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, "INSERT INTO events(id,ts,actor,role,verb,object_key,outcome,code,detail_json) VALUES(?,?,?,?,?,?,?,?,?)", want.ID, want.TS, want.Actor, want.Role, want.Verb, want.ObjectKey, want.Outcome, want.Code, string(want.Detail)); err != nil {
				return err
			}
		}
		if present {
			result, err := tx.ExecContext(ctx, "DELETE FROM fw_pending WHERE id=? AND revert_unit=? AND deadline=? AND confirmed=0", r.ID, r.Unit, want.TS)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return errors.New("firewall projection not consumed")
			}
		}
		return nil
	})
}

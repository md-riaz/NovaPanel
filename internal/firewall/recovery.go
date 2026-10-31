package firewall

import (
	"context"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"time"
)

// Pending mirrors fw_pending. ID itself is the one-shot confirmation nonce.
type Pending struct {
	ID           string
	SnapshotPath string
	RevertUnit   string
	Deadline     time.Time
}

// PendingStore must serialize this callback across ALL processes, including the
// independent root deadman, and durably delete the row only when consume is true.
// A restore/cancel or persistence failure must retain recoverable pending state.
// Do not implement this with a daemon-only mutex.
type PendingStore interface {
	WithPending(context.Context, string, func(Pending) (consume bool, err error)) error
}

// SnapshotRecovery restores the saved UFW rules AND enabled/disabled state,
// then durably writes the revert audit marker before returning success. It must
// be idempotent and validate snapshot paths against the agent-owned snapshot root.
type SnapshotRecovery interface {
	RestoreAndMark(context.Context, Pending) error
}

type Recovery struct {
	Store    PendingStore
	Runner   execpkg.Runner
	Snapshot SnapshotRecovery
	Now      func() time.Time
}

func notFound() error { return rpcerr.Errf("not_found", "firewall pending not found or expired") }

// Confirm is synchronous. Its caller must authenticate a fresh request and apply
// the canonical admin+/MFA or automation firewall:write authorization gate.
func (r *Recovery) Confirm(ctx context.Context, pendingID string) error {
	return r.with(ctx, pendingID, func(p Pending) (bool, error) {
		if !r.Now().Before(p.Deadline) {
			return false, notFound()
		}
		_, _, exit, err := r.Runner.Run(ctx, "/usr/bin/systemctl", []string{"stop", p.RevertUnit + ".timer"}, execpkg.RunOpts{})
		if err != nil || exit != 0 {
			return false, rpcerr.Errf("agent_error", "firewall timer cancellation failed")
		}
		return true, nil
	})
}
func (r *Recovery) Revert(ctx context.Context, pendingID string) error {
	return r.with(ctx, pendingID, func(p Pending) (bool, error) {
		if !r.Now().Before(p.Deadline) {
			return false, notFound()
		}
		return r.restore(ctx, p)
	})
}

// Fire is the independent deadman's entrypoint. Expired rows remain available to
// this method even though client confirm/revert must return not_found.
func (r *Recovery) Fire(ctx context.Context, pendingID string) error {
	return r.with(ctx, pendingID, func(p Pending) (bool, error) {
		if r.Now().Before(p.Deadline) {
			return false, rpcerr.Errf("precondition_failed", "firewall deadline not reached")
		}
		return r.restore(ctx, p)
	})
}
func (r *Recovery) restore(ctx context.Context, p Pending) (bool, error) {
	if err := r.Snapshot.RestoreAndMark(ctx, p); err != nil {
		return false, rpcerr.Errf("agent_error", "firewall recovery failed")
	}
	return true, nil
}
func (r *Recovery) with(ctx context.Context, pendingID string, fn func(Pending) (bool, error)) error {
	if !id.Valid(pendingID) {
		return notFound()
	}
	if r.Store == nil || r.Runner == nil || r.Snapshot == nil || r.Now == nil {
		return rpcerr.Errf("agent_error", "firewall recovery dependencies missing")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.Store.WithPending(ctx, pendingID, func(p Pending) (bool, error) {
		if p.ID != pendingID || p.RevertUnit != "novapanel-fwrevert-"+pendingID || p.SnapshotPath == "" || p.Deadline.IsZero() {
			return false, rpcerr.Errf("agent_error", "invalid firewall pending state")
		}
		return fn(p)
	})
}

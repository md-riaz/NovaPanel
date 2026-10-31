package firewall

import (
	"context"
	"errors"
	"os"
)

// OpenInstalledFirewallJournal admits only the fixed preprovisioned root layout.
// It neither provisions custody nor opens SQLite or a restoration destination.
func OpenInstalledFirewallJournal(ctx context.Context) (*Journal, error) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return nil, errRuntimeRecovery
	}
	l := runtimeLayout{root: productionFirewallRoot}
	if err := l.admit(); err != nil {
		return nil, err
	}
	return OpenJournalContext(ctx, l.root+"/journal")
}

// CommitTerminal holds authoritative journal custody through commit without
// acknowledging or changing the terminal evidence. Callbacks must not reenter.
func (j *Journal) CommitTerminal(ctx context.Context, commit func(JournalRecord) error) error {
	if commit == nil {
		return errors.New("missing terminal commit adapter")
	}
	return j.locked(ctx, func() error {
		r, err := j.read()
		if err != nil {
			return err
		}
		if r.State != "confirmed" && r.State != "reverted" {
			return errors.New("journal not terminal")
		}
		if err = j.syncDir(); err != nil {
			if r.State == "confirmed" {
				return errors.Join(ErrConfirmationCommitted, err)
			}
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return commit(r)
	})
}

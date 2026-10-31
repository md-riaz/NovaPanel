package firewall

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
	"os"
	"time"
)

var ErrFirewallRecoveryPending = errors.New("firewall recovery pending; terminal reconciliation and reuse unavailable")

// FirewallStartupPlan owns pinned custody and a classification, never a caller
// supplied permission bit. Close must follow completion, not race it.
type FirewallStartupPlan struct {
	journal *Journal
	record  JournalRecord
	present bool
}

func (p *FirewallStartupPlan) Close() error {
	if p == nil || p.journal == nil {
		return errors.New("missing firewall startup plan")
	}
	return p.journal.Close()
}

func PrepareInstalledFirewallStartup(ctx context.Context, runner execpkg.Runner) (*FirewallStartupPlan, error) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return nil, fmt.Errorf("firewall startup: %w", errRuntimeRecovery)
	}
	return prepareFirewallStartup(ctx, runtimeLayout{root: productionFirewallRoot, rules: "/etc/ufw"}, runtimeManager{runner: runner, now: time.Now, monotonic: managerMonotonic})
}
func prepareFirewallStartup(ctx context.Context, l runtimeLayout, m runtimeManager) (plan *FirewallStartupPlan, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	defer func() {
		if err != nil {
			err = fmt.Errorf("firewall startup: %w", err)
		}
	}()
	if err = l.admit(); err != nil {
		return nil, err
	}
	j, err := OpenJournalContext(ctx, l.root+"/journal")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, j.Close())
		}
	}()
	r, present, err := j.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return &FirewallStartupPlan{journal: j}, nil
	}
	switch r.State {
	case "acknowledged":
		if r.Version != 2 {
			return nil, ErrLegacyAcknowledgement
		}
		fallthrough
	case "confirmed", "reverted":
		return &FirewallStartupPlan{journal: j, record: r, present: true}, nil
	}
	managerErr := m.ensure(ctx, r)
	after, present, inspectErr := j.Inspect(ctx)
	if inspectErr != nil {
		return nil, errors.Join(ErrFirewallRecoveryPending, managerErr, inspectErr)
	}
	if !present || after != r {
		return nil, errors.Join(ErrFirewallRecoveryPending, managerErr, errors.New("journal changed during manager verification"))
	}
	return nil, errors.Join(ErrFirewallRecoveryPending, managerErr)
}

// Reconcile holds the classified journal through transaction and publication.
// Caller takes Audit before entering. Store callbacks never reenter either lock.
func (p *FirewallStartupPlan) Reconcile(ctx context.Context, commit, verify func(JournalRecord) error, absent func() error) error {
	if p == nil || p.journal == nil {
		return errors.New("missing firewall startup plan")
	}
	if p.present {
		return p.journal.reconcileExpected(ctx, &p.record, commit, verify)
	}
	if absent == nil {
		return errors.New("missing absence verifier")
	}
	return p.journal.locked(ctx, func() error {
		if _, err := p.journal.read(); !errors.Is(err, unix.ENOENT) {
			if err != nil {
				return err
			}
			return errors.New("firewall journal appeared during startup")
		}
		if err := absent(); err != nil {
			return err
		}
		return ctx.Err()
	})
}

// Legacy preflight remains fail-closed for every retained record.
func PreflightInstalledFirewall(ctx context.Context, runner execpkg.Runner) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return fmt.Errorf("firewall startup: %w", errRuntimeRecovery)
	}
	return startupFirewall(ctx, runtimeLayout{root: productionFirewallRoot, rules: "/etc/ufw"}, runtimeManager{runner: runner, now: time.Now, monotonic: managerMonotonic})
}
func startupFirewall(ctx context.Context, l runtimeLayout, m runtimeManager) (err error) {
	p, err := prepareFirewallStartup(ctx, l, m)
	if err != nil {
		if errors.Is(err, ErrLegacyAcknowledgement) {
			return errors.Join(ErrFirewallRecoveryPending, err)
		}
		return err
	}
	defer func() { err = errors.Join(err, p.Close()) }()
	if p.present {
		return ErrFirewallRecoveryPending
	}
	return nil
}

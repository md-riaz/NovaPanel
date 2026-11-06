package firewall

import (
	"context"
	"errors"
	"os"
	"time"

	execpkg "novapanel/internal/exec"
	"novapanel/internal/id"
)

// JournalRecovery is the internal synchronous consumer of the recovery journal.
// It neither reconciles SQLite nor authorizes public requests. Callers must not
// call Close concurrently with operations.
type JournalRecovery struct {
	journal *Journal
	custody *SnapshotCustody
	layout  runtimeLayout
	now     func() time.Time
	manager runtimeManager
	runner  execpkg.Runner
}

// OpenInstalledJournalRecovery opens only the fixed, preprovisioned root layout.
// Initial journal lock admission honors ctx; no production path override exists.
func OpenInstalledJournalRecovery(ctx context.Context) (*JournalRecovery, error) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return nil, errRuntimeRecovery
	}
	return openInstalledJournalRecovery(ctx, runtimeLayout{root: productionFirewallRoot, rules: "/etc/ufw"}, time.Now, execpkg.Real{}, execpkg.Recovery{})
}

func openInstalledJournalRecovery(ctx context.Context, l runtimeLayout, now func() time.Time, manager, runner execpkg.Runner) (*JournalRecovery, error) {
	if now == nil || manager == nil || runner == nil {
		return nil, errRuntimeRecovery
	}
	if e := l.admit(); e != nil {
		return nil, e
	}
	j, e := openJournalContext(ctx, l.root+"/journal")
	if e != nil {
		return nil, errors.Join(errRuntimeRecovery, e)
	}
	c, e := OpenSnapshotCustody(l.root + "/snapshots")
	if e != nil {
		return nil, errors.Join(errRuntimeRecovery, e, j.Close())
	}
	return &JournalRecovery{journal: j, custody: c, layout: l, now: now, manager: runtimeManager{runner: manager}, runner: runner}, nil
}

func (s *JournalRecovery) Close() error { return errors.Join(s.custody.Close(), s.journal.Close()) }

func (s *JournalRecovery) Confirm(ctx context.Context, pendingID string) error {
	if !id.Valid(pendingID) {
		return notFound()
	}
	return s.journal.Confirm(ctx, pendingID, s.now, func(stem string) error {
		completion, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return s.manager.stop(completion, stem)
	})
}

func (s *JournalRecovery) Revert(ctx context.Context, pendingID string) error {
	if !id.Valid(pendingID) {
		return notFound()
	}
	return s.journal.RevertVerified(ctx, pendingID, s.now, s.custody, installedRestoreCallback(ctx, s.layout, s.runner))
}

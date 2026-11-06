package firewall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
	"novapanel/internal/id"
)

var errRuntimeRecovery = errors.New("firewall recovery layout refused")

const productionFirewallRoot = "/var/lib/novapanel/firewall"

type runtimeLayout struct{ root, rules string }

// strictRuntimeDir admits every ancestor, including /, without sticky exceptions.
// The runtime has no provisioning or permission-repair authority.
func strictRuntimeDir(path string, private bool) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errRuntimeRecovery
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errRuntimeRecovery
	}
	check := func() bool {
		var s unix.Stat_t
		return unix.Fstat(fd, &s) == nil && s.Uid == 0 && s.Gid == 0 && s.Mode&0022 == 0
	}
	if !check() {
		unix.Close(fd)
		return nil, errRuntimeRecovery
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, errRuntimeRecovery
		}
		fd = next
		if !check() {
			unix.Close(fd)
			return nil, errRuntimeRecovery
		}
	}
	var s unix.Stat_t
	if unix.Fstat(fd, &s) != nil || (private && s.Mode&07777 != 0700) {
		unix.Close(fd)
		return nil, errRuntimeRecovery
	}
	return os.NewFile(uintptr(fd), "runtime-directory"), nil
}
func (l runtimeLayout) admit() error {
	for _, p := range []string{l.root, l.root + "/journal", l.root + "/snapshots"} {
		f, e := strictRuntimeDir(p, true)
		if e != nil {
			return errRuntimeRecovery
		}
		if f.Close() != nil {
			return errRuntimeRecovery
		}
	}
	d, e := strictRuntimeDir(l.root+"/journal", true)
	if e != nil {
		return e
	}
	defer d.Close()
	f, e := journalFile(int(d.Fd()), "lock", unix.O_RDONLY)
	if e != nil {
		return errRuntimeRecovery
	}
	defer f.Close()
	var s unix.Stat_t
	if unix.Fstat(int(f.Fd()), &s) != nil || s.Gid != 0 || s.Size != 0 {
		return errRuntimeRecovery
	}
	return nil
}

// RecoverInstalledFirewall is the fixed-path, independent managed-service entry.
// It never opens SQLite, config, audit or daemon state, nor provisions custody.
func RecoverInstalledFirewall(ctx context.Context, pendingID string) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 || !id.Valid(pendingID) {
		return errRuntimeRecovery
	}
	if e := admitRecoveryService(pendingID); e != nil {
		return e
	}
	return recoverInstalled(ctx, pendingID, runtimeLayout{root: productionFirewallRoot, rules: "/etc/ufw"}, time.Now, execpkg.Recovery{})
}

func recoverInstalled(ctx context.Context, pendingID string, l runtimeLayout, now func() time.Time, runner execpkg.Runner) (err error) {
	if !id.Valid(pendingID) || now == nil || runner == nil {
		return errRuntimeRecovery
	}
	if err = l.admit(); err != nil {
		return err
	}
	j, err := openJournalContext(ctx, l.root+"/journal")
	if err != nil {
		return errors.Join(errRuntimeRecovery, err)
	}
	defer func() { err = errors.Join(err, j.Close()) }()
	c, err := OpenSnapshotCustody(l.root + "/snapshots")
	if err != nil {
		return errRuntimeRecovery
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	return j.FireVerified(ctx, pendingID, now, c, installedRestoreCallback(ctx, l, runner))
}

// installedRestoreCallback keeps destination admission lazy for terminal journal
// decisions, and shares the exact descriptor restoration between Fire and Revert.
func installedRestoreCallback(ctx context.Context, l runtimeLayout, runner execpkg.Runner) VerifiedRestore {
	return func(r JournalRecord, s *VerifiedSnapshot) (err error) {
		if err = l.admit(); err != nil {
			return err
		}
		for _, path := range []string{l.root + "/restore", l.root + "/restore/markers"} {
			d, e := strictRuntimeDir(path, true)
			if e != nil {
				return e
			}
			if e = d.Close(); e != nil {
				return e
			}
		}
		a, err := openInstalledRestore(l.rules, l.root+"/restore", 0, runner)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, a.Close()) }()
		return a.Restore(ctx, r, s)
	}
}

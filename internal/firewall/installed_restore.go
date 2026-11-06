package firewall

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
)

var errInstalledRestore = errors.New("installed firewall restoration refused")

// InstalledRestore restores the closed rule inventory in the installed layout.
// It is not wired into runtime recovery. Close must not race Restore. The trusted
// owner must not mutate admitted paths concurrently. Tests must use a fake Runner.
type InstalledRestore struct {
	rulesRoot, privateRoot  string
	owner                   uint32
	rules, private, markers *os.File
	runner                  execpkg.Runner
}

// OpenInstalledRestore never provisions paths or accepts caller-selected roots.
func OpenInstalledRestore(runner execpkg.Runner) (*InstalledRestore, error) {
	if os.Geteuid() != 0 {
		return nil, errInstalledRestore
	}
	return openInstalledRestore("/etc/ufw", "/var/lib/novapanel/firewall/restore", 0, runner)
}
func openInstalledRestore(rules, private string, owner uint32, runner execpkg.Runner) (*InstalledRestore, error) {
	if runner == nil {
		return nil, errInstalledRestore
	}
	rd, e := captureRoot(rules, owner)
	if e != nil {
		return nil, errInstalledRestore
	}
	pd, e := captureRoot(private, owner)
	if e != nil {
		rd.Close()
		return nil, errInstalledRestore
	}
	a := &InstalledRestore{rulesRoot: rules, privateRoot: private, owner: owner, rules: rd, private: pd, runner: runner}
	a.markers, e = a.privateDir(pd, "markers")
	if e != nil || !a.privateMode(pd) {
		if a.markers != nil {
			a.markers.Close()
		}
		pd.Close()
		rd.Close()
		return nil, errInstalledRestore
	}
	return a, nil
}
func (a *InstalledRestore) Close() error {
	return errors.Join(a.rules.Close(), a.private.Close(), a.markers.Close())
}
func (a *InstalledRestore) privateMode(f *os.File) bool {
	var st unix.Stat_t
	return unix.Fstat(int(f.Fd()), &st) == nil && st.Uid == a.owner && st.Mode&07777 == 0700
}
func (a *InstalledRestore) privateDir(d *os.File, n string) (*os.File, error) {
	fd, e := unix.Openat(int(d.Fd()), n, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errInstalledRestore
	}
	f := os.NewFile(uintptr(fd), n)
	if !a.privateMode(f) {
		f.Close()
		return nil, errInstalledRestore
	}
	return f, nil
}
func installedSame(a, b *os.File) bool {
	x, e := a.Stat()
	y, f := b.Stat()
	return e == nil && f == nil && os.SameFile(x, y)
}
func (a *InstalledRestore) check(ctx context.Context) error {
	if ctx.Err() != nil {
		return errInstalledRestore
	}
	rd, e := captureRoot(a.rulesRoot, a.owner)
	if e != nil {
		return errInstalledRestore
	}
	defer rd.Close()
	pd, e := captureRoot(a.privateRoot, a.owner)
	if e != nil {
		return errInstalledRestore
	}
	defer pd.Close()
	md, e := a.privateDir(pd, "markers")
	if e != nil {
		return errInstalledRestore
	}
	defer md.Close()
	if !a.privateMode(pd) || !installedSame(rd, a.rules) || !installedSame(pd, a.private) || !installedSame(md, a.markers) {
		return errInstalledRestore
	}
	return nil
}

// leaf never opens an installed object writable and bounds special-file opens.
func (a *InstalledRestore) leaf(d *os.File, name string, private bool) (*os.File, error) {
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(e, unix.ENOENT) {
		return nil, nil
	}
	if e != nil {
		return nil, errInstalledRestore
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != a.owner || st.Gid != 0 || st.Nlink != 1 || (st.Mode&07777 != 0600 && (private || (st.Mode&07777 != 0640 && st.Mode&07777 != 0644))) {
		f.Close()
		return nil, errInstalledRestore
	}
	return f, nil
}
func (a *InstalledRestore) admit(d *os.File, name string, private bool) (os.FileInfo, error) {
	f, e := a.leaf(d, name, private)
	if e != nil || f == nil {
		return nil, e
	}
	st, e := f.Stat()
	ce := f.Close()
	return st, errors.Join(e, ce)
}
func (a *InstalledRestore) unchanged(d *os.File, name string, private bool, want os.FileInfo) bool {
	now, e := a.admit(d, name, private)
	if e != nil {
		return false
	}
	if now == nil || want == nil {
		return now == nil && want == nil
	}
	return os.SameFile(now, want) && now.Mode() == want.Mode()
}
func (a *InstalledRestore) publish(ctx context.Context, d *os.File, name, stage string, b []byte, mode os.FileMode, private bool, admitted map[string]os.FileInfo) error {
	if a.check(ctx) != nil {
		return errInstalledRestore
	}
	if !a.unchanged(d, name, private, admitted[name]) || !a.unchanged(d, stage, private, admitted[stage]) {
		return errInstalledRestore
	}
	if e := unix.Unlinkat(int(d.Fd()), stage, 0); e != nil && e != unix.ENOENT {
		return errInstalledRestore
	}
	fd, e := unix.Openat(int(d.Fd()), stage, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return errInstalledRestore
	}
	f := os.NewFile(uintptr(fd), stage)
	n, e := f.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Chmod(mode)
	}
	if e == nil {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != a.owner || st.Gid != 0 || st.Nlink != 1 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != uint32(mode) {
			e = errInstalledRestore
		}
	}
	if e == nil {
		e = f.Sync()
	}
	staged, se := f.Stat()
	ce := f.Close()
	if e != nil || se != nil || ce != nil || a.check(ctx) != nil || !a.unchanged(d, name, private, admitted[name]) || !a.unchanged(d, stage, private, staged) {
		return errInstalledRestore
	}
	if unix.Renameat(int(d.Fd()), stage, int(d.Fd()), name) != nil || d.Sync() != nil || a.check(ctx) != nil || !a.unchanged(d, name, private, staged) {
		return errInstalledRestore
	}
	admitted[name] = staged
	admitted[stage] = nil
	return nil
}

// Restore is called under Journal.FireVerified or RevertVerified. A matching
// marker never suppresses replay. Errors after effects require same-ID recovery.
func (a *InstalledRestore) Restore(ctx context.Context, r JournalRecord, s *VerifiedSnapshot) error {
	data, marker, e := restoreData(r, s)
	if e != nil || a.check(ctx) != nil {
		return errInstalledRestore
	}
	lock, e := a.privateDir(a.private, ".")
	if e != nil {
		return errInstalledRestore
	}
	defer lock.Close()
	for {
		e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if e != unix.EAGAIN && e != unix.EWOULDBLOCK {
			return errInstalledRestore
		}
		select {
		case <-ctx.Done():
			return errInstalledRestore
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if a.check(ctx) != nil {
		return errInstalledRestore
	}
	admitted := map[string]os.FileInfo{}
	for _, n := range snapshotNames[4:] {
		name := strings.TrimPrefix(n, "ufw/")
		for _, leaf := range []string{name, name + ".novapanel-next"} {
			st, e := a.admit(a.rules, leaf, false)
			if e != nil {
				return errInstalledRestore
			}
			admitted[leaf] = st
		}
	}
	for _, leaf := range []string{r.ID + ".json", r.ID + ".json.next"} {
		st, e := a.admit(a.markers, leaf, true)
		if e != nil {
			return errInstalledRestore
		}
		admitted[leaf] = st
	}
	if f, e := a.leaf(a.markers, r.ID+".json", true); e != nil {
		return errInstalledRestore
	} else if f != nil {
		b, re := snapshotRead(f, journalLimit)
		ce := f.Close()
		if re != nil || ce != nil || !bytes.Equal(b, marker) {
			return errInstalledRestore
		}
	}
	modes := map[string]os.FileMode{}
	for i, n := range snapshotNames[4:] {
		name := strings.TrimPrefix(n, "ufw/")
		mode := os.FileMode(0600)
		if st := admitted[name]; st != nil {
			mode = st.Mode().Perm()
		}
		modes[name] = mode
		for _, previous := range snapshotNames[4 : 4+i] {
			leaf := strings.TrimPrefix(previous, "ufw/")
			if !a.unchanged(a.rules, leaf, false, admitted[leaf]) {
				return errInstalledRestore
			}
		}
		if a.publish(ctx, a.rules, name, name+".novapanel-next", data[i], mode, false, admitted) != nil {
			return errInstalledRestore
		}
	}
	for _, n := range snapshotNames[4:] {
		leaf := strings.TrimPrefix(n, "ufw/")
		if !a.unchanged(a.rules, leaf, false, admitted[leaf]) {
			return errInstalledRestore
		}
	}
	plan := [][]string{{"disable"}}
	if r.PriorEnabled {
		plan = [][]string{{"--force", "enable"}, {"reload"}}
	}
	for _, args := range plan {
		if a.check(ctx) != nil {
			return errInstalledRestore
		}
		_, _, exit, e := a.runner.Run(ctx, "/usr/sbin/ufw", args, execpkg.RunOpts{Env: []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}})
		if e != nil || exit != 0 || a.check(ctx) != nil {
			return errInstalledRestore
		}
	}
	for i, n := range snapshotNames[4:] {
		name := strings.TrimPrefix(n, "ufw/")
		f, e := a.leaf(a.rules, name, false)
		if e != nil || f == nil {
			return errInstalledRestore
		}
		st, se := f.Stat()
		b, re := restoreRead(f, int64(len(data[i])))
		if se != nil || re != nil || st.Mode().Perm() != modes[name] || !bytes.Equal(b, data[i]) {
			f.Close()
			return errInstalledRestore
		}
		syncErr := f.Sync()
		ce := f.Close()
		if syncErr != nil || ce != nil || a.check(ctx) != nil || !a.unchanged(a.rules, name, false, st) {
			return errInstalledRestore
		}
	}
	if a.rules.Sync() != nil || a.check(ctx) != nil {
		return errInstalledRestore
	}
	return a.publish(ctx, a.markers, r.ID+".json", r.ID+".json.next", marker, 0600, true, admitted)
}

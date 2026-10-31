//go:build linux

package firewall

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
)

var errRecoveryService = errors.New("fw-revert requires its independent managed recovery service")

func recoveryMembership(b []byte, pendingID string) (string, error) {
	expected := "/system.slice/novapanel-fwrevert-" + pendingID + ".service"
	if !id.Valid(pendingID) || string(b) != "0::"+expected+"\n" {
		return "", errRecoveryService
	}
	return expected, nil
}
func readServiceLeaf(d *os.File, name string, limit int64) ([]byte, error) {
	fd, e := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errRecoveryService
	}
	f := os.NewFile(uintptr(fd), "service-evidence")
	defer f.Close()
	var s unix.Stat_t
	if unix.Fstat(fd, &s) != nil || s.Mode&unix.S_IFMT != unix.S_IFREG || s.Uid != 0 || s.Gid != 0 || s.Mode&0022 != 0 {
		return nil, errRecoveryService
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errRecoveryService
	}
	return b, nil
}
func admitRecoveryService(pendingID string) error {
	// No environment variable is service evidence. Refuse user managers, cgroup v1,
	// nested delegation and namespaces that hide the exact system.slice identity.
	manager, e := strictRuntimeDir("/run/systemd/system", false)
	if e != nil {
		return errRecoveryService
	}
	defer manager.Close()
	proc, e := strictRuntimeDir("/proc/"+strconv.Itoa(os.Getpid()), false)
	if e != nil {
		return errRecoveryService
	}
	defer proc.Close()
	var fs unix.Statfs_t
	if unix.Fstatfs(int(proc.Fd()), &fs) != nil || fs.Type != unix.PROC_SUPER_MAGIC {
		return errRecoveryService
	}
	membership, e := readServiceLeaf(proc, "cgroup", 4096)
	if e != nil {
		return e
	}
	path, e := recoveryMembership(membership, pendingID)
	if e != nil {
		return e
	}
	root, e := strictRuntimeDir("/sys/fs/cgroup", false)
	if e != nil {
		return errRecoveryService
	}
	defer root.Close()
	if unix.Fstatfs(int(root.Fd()), &fs) != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return errRecoveryService
	}
	group, e := strictRuntimeDir("/sys/fs/cgroup"+path, false)
	if e != nil {
		return errRecoveryService
	}
	defer group.Close()
	typ, e := readServiceLeaf(group, "cgroup.type", 64)
	if e != nil || string(typ) != "domain\n" {
		return errRecoveryService
	}
	procs, e := readServiceLeaf(group, "cgroup.procs", 1<<20)
	if e != nil {
		return e
	}
	found := false
	for _, p := range strings.Fields(string(procs)) {
		if p == strconv.Itoa(os.Getpid()) {
			found = true
		}
	}
	again, e := readServiceLeaf(proc, "cgroup", 4096)
	if e != nil || !bytes.Equal(membership, again) || !found {
		return errRecoveryService
	}
	// Reopen and compare identities to reject replaced/moved cgroup roots.
	for path, pinned := range map[string]*os.File{"/sys/fs/cgroup": root, "/sys/fs/cgroup" + path: group, "/run/systemd/system": manager} {
		current, e := strictRuntimeDir(path, false)
		if e != nil {
			return errRecoveryService
		}
		same := installedSame(current, pinned)
		ce := current.Close()
		if !same || ce != nil {
			return errRecoveryService
		}
	}
	return nil
}

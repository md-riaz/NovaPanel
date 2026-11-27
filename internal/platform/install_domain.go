package platform

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/installgate"
)

var installOwners = struct {
	sync.Mutex
	paths  map[string]*ExecutionDomain
	active map[string]int
}{paths: make(map[string]*ExecutionDomain), active: make(map[string]int)}

// InstallDomain holds an install cgroup; it does not attest account freshness,
// close other launchers or exclude account and filesystem changes.
type InstallDomain struct {
	gate        installgate.Gate
	domain      *ExecutionDomain
	parent      *os.File
	name        string
	detached    atomic.Bool
	closeDomain func() error
}

// CreateInstallDomain creates or reopens the site's empty domain below the
// agent's delegated cgroup. Trusted root must exclude replacement throughout.
func CreateInstallDomain(siteID string) (*InstallDomain, error) {
	if !id.Valid(siteID) {
		return nil, errDomain
	}
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	own, err := ownCgroup(string(raw))
	if err != nil {
		return nil, err
	}
	path := filepath.Join("/sys/fs/cgroup", own)
	domainPath := filepath.Join(path, "novapanel-install", siteID)
	installOwners.Lock()
	conflict := false
	for path := range installOwners.paths {
		if overlapsDomain(path, domainPath) {
			conflict = true
			break
		}
	}
	for path := range installOwners.active {
		if overlapsDomain(path, domainPath) {
			conflict = true
			break
		}
	}
	if conflict {
		installOwners.Unlock()
		return nil, fmt.Errorf("%w: site install domain already owned or active", errDomain)
	}
	installOwners.paths[domainPath] = nil
	installOwners.Unlock()
	owned := false
	defer func() {
		if !owned {
			installOwners.Lock()
			delete(installOwners.paths, domainPath)
			installOwners.Unlock()
		}
	}()
	fd, err := unix.Open(path, unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open delegated cgroup: %v", errDomain, err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 || unix.Fstatfs(fd, &fs) != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, fmt.Errorf("%w: delegated cgroup ownership, mode or filesystem invalid", errDomain)
	}
	if !cgroupContainsSelf(fd) {
		return nil, fmt.Errorf("%w: discovered cgroup does not contain agent", errDomain)
	}
	if err := unix.Mkdirat(fd, "novapanel-install", 0755); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, fmt.Errorf("%w: delegated cgroup is not writable: %v", errDomain, err)
	}
	pfd, err := unix.Openat(fd, "novapanel-install", unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(pfd), "novapanel-install")
	if unix.Fstat(pfd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 {
		parent.Close()
		return nil, fmt.Errorf("%w: install parent ownership or mode invalid", errDomain)
	}
	if err := unix.Mkdirat(pfd, siteID, 0755); err != nil {
		if !errors.Is(err, unix.EEXIST) {
			parent.Close()
			return nil, fmt.Errorf("%w: create install domain: %v", errDomain, err)
		}
		// A crash remnant is usable only after OpenExecutionDomain validates
		// root ownership, control files, domain type and recursive emptiness.
	}
	d, err := OpenExecutionDomain(domainPath)
	if err != nil {
		parent.Close()
		return nil, err
	}
	handle := &InstallDomain{domain: d, parent: parent, name: siteID, closeDomain: d.Close}
	if err := handle.empty(); err != nil {
		d.Close()
		parent.Close()
		return nil, err
	}
	installOwners.Lock()
	installOwners.paths[domainPath] = d
	installOwners.Unlock()
	owned = true
	return handle, nil
}

func cgroupContainsSelf(fd int) bool {
	cfd, err := unix.Openat(fd, "cgroup.procs", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	f := os.NewFile(uintptr(cfd), "cgroup.procs")
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return false
	}
	pid := strconv.Itoa(os.Getpid())
	for _, entry := range strings.Fields(string(raw)) {
		if entry == pid {
			return true
		}
	}
	return false
}

func ownCgroup(raw string) (string, error) {
	var path string
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		if strings.HasPrefix(line, "0::") {
			if path != "" {
				return "", errDomain
			}
			path = strings.TrimPrefix(line, "0::")
		}
	}
	if path == "" || path == "/" || !strings.HasPrefix(path, "/") || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("%w: invalid or root agent cgroup", errDomain)
	}
	return path, nil
}

// Empty is an emptiness assertion for fresh accounts, not an IO-retirement proof.
func (d *InstallDomain) Empty() error {
	return d.gate.Check(d.empty)
}
func (d *InstallDomain) empty() error {
	empty, err := d.domain.empty()
	if err != nil {
		return err
	}
	if !empty {
		return errDomain
	}
	return d.leaf()
}

// leaf rejects even unpopulated child cgroups; recursive emptiness alone does
// not prevent a separate child-domain handle from admitting a process.
func (d *InstallDomain) leaf() error {
	fd, err := unix.Openat(int(d.domain.file.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), "install-domain-leaf")
	defer dir.Close()
	for {
		names, err := dir.Readdirnames(64)
		for _, name := range names {
			var st unix.Stat_t
			if unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
				return errDomain
			}
			if st.Mode&unix.S_IFMT == unix.S_IFDIR {
				return fmt.Errorf("%w: install domain is not a leaf", errDomain)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// WithEmpty holds this domain's admission gate around a synchronous trusted-root
// effect. The caller must also hold account and trusted-root exclusion.
func (d *InstallDomain) WithEmpty(ctx context.Context, effect func(context.Context) error) error {
	if effect == nil {
		return errDomain
	}
	return d.gate.Do(d.empty, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, done, err := d.domain.admit(ctx)
		if err != nil {
			return err
		}
		defer done()
		return errors.Join(effect(work), work.Err())
	})
}

// Run admits one reviewed child at birth and checks recursive emptiness around it.
func (d *InstallDomain) Run(ctx context.Context, program string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	var out, stderr []byte
	var childErr error
	code := -1
	err := d.gate.Do(d.empty, func() error {
		out, stderr, code, childErr = d.domain.Run(ctx, program, args, opts)
		if runner.NormalExit(childErr, code) {
			return nil
		}
		return childErr
	})
	if err != nil {
		return out, stderr, code, err
	}
	return out, stderr, code, childErr
}

// Close drains before removing the site domain. An unsuccessful drain retains it.
func (d *InstallDomain) Close(ctx context.Context) error {
	return d.gate.Close(func() error { return d.domain.Drain(ctx) }, func() error {
		if d.detached.Swap(true) {
			return errDomain
		}
		// Once drain succeeds no admitted effect survives, even if removal or
		// descriptor close fails; allow a validated reopen of the remnant.
		defer func() {
			installOwners.Lock()
			if installOwners.paths[d.domain.path] == d.domain {
				delete(installOwners.paths, d.domain.path)
			}
			installOwners.Unlock()
		}()
		unlinkErr := unix.Unlinkat(int(d.parent.Fd()), d.name, unix.AT_REMOVEDIR)
		return errors.Join(unlinkErr, d.closeDomain(), d.parent.Close())
	})
}

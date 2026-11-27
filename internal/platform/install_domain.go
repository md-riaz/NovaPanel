package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/id"
)

// InstallDomain holds an install cgroup; it does not attest account freshness,
// close other launchers or exclude account and filesystem changes.
type InstallDomain struct {
	mu      sync.Mutex
	closeMu sync.Mutex
	closing atomic.Bool
	domain  *ExecutionDomain
	parent  *os.File
	name    string
	closed  bool
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
	fd, err := unix.Open(path, unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: open delegated cgroup: %v", errDomain, err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 || unix.Fstatfs(fd, &fs) != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, errDomain
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
		return nil, errDomain
	}
	if err := unix.Mkdirat(pfd, siteID, 0755); err != nil && !errors.Is(err, unix.EEXIST) {
		parent.Close()
		return nil, fmt.Errorf("%w: create install domain: %v", errDomain, err)
	}
	d, err := OpenExecutionDomain(filepath.Join(path, "novapanel-install", siteID))
	if err != nil {
		parent.Close()
		return nil, err
	}
	return &InstallDomain{domain: d, parent: parent, name: siteID}, nil
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
	if path == "" || !strings.HasPrefix(path, "/") || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
		return "", errDomain
	}
	return path, nil
}

// Empty is an emptiness assertion for fresh accounts, not an IO-retirement proof.
func (d *InstallDomain) Empty() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.empty()
}
func (d *InstallDomain) empty() error {
	if d.closed || d.closing.Load() {
		return errDomain
	}
	empty, err := d.domain.empty()
	if err != nil {
		return err
	}
	if !empty {
		return errDomain
	}
	return nil
}

// WithEmpty holds this domain's admission gate around a synchronous trusted-root
// effect. The caller must also hold account and trusted-root exclusion.
func (d *InstallDomain) WithEmpty(ctx context.Context, effect func(context.Context) error) error {
	if effect == nil {
		return errDomain
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.empty(); err != nil {
		return err
	}
	work, done, err := d.domain.admit(ctx)
	if err != nil {
		return err
	}
	defer done()
	err = effect(work)
	return errors.Join(err, work.Err(), d.empty())
}

// Run admits one reviewed child at birth and checks recursive emptiness around it.
func (d *InstallDomain) Run(ctx context.Context, program string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.empty(); err != nil {
		return nil, nil, -1, err
	}
	out, stderr, code, err := d.domain.Run(ctx, program, args, opts)
	return out, stderr, code, errors.Join(err, d.empty())
}

// Close drains before removing the site domain. An unsuccessful drain retains it.
func (d *InstallDomain) Close(ctx context.Context) error {
	d.closeMu.Lock()
	defer d.closeMu.Unlock()
	d.closing.Store(true)
	if d.closed {
		return errDomain
	}
	if err := d.domain.Drain(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := unix.Unlinkat(int(d.parent.Fd()), d.name, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	if err := d.domain.Close(); err != nil {
		return err
	}
	d.closed = true
	return d.parent.Close()
}

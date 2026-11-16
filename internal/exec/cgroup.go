package exec

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// Cgroup is a held Linux cgroup v2 directory capability, not a lifetime or
// quiescence attestation. Close revokes future runs, not already admitted runs.
// The root supervisor must exclude migration, replacement and foreign launch.
type Cgroup struct {
	mu   sync.Mutex
	file *os.File
}

// NewCgroup duplicates f; callers retain ownership of f. Directory identity is
// retained rather than reopening a pathname at process creation.
func NewCgroup(f *os.File) (*Cgroup, error) {
	if f == nil {
		return nil, errors.New("exec: missing cgroup")
	}
	raw, err := f.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var dupErr error
	err = raw.Control(func(n uintptr) { fd, dupErr = unix.FcntlInt(n, unix.F_DUPFD_CLOEXEC, 0) })
	if err != nil {
		return nil, err
	}
	if dupErr != nil {
		return nil, dupErr
	}
	held := os.NewFile(uintptr(fd), "cgroup")
	var fs unix.Statfs_t
	var st unix.Stat_t
	if unix.Fstatfs(fd, &fs) != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC || unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		held.Close()
		return nil, errors.New("exec: not a cgroup v2 directory")
	}
	return &Cgroup{file: held}, nil
}
func (c *Cgroup) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil {
		return nil
	}
	err := c.file.Close()
	c.file = nil
	return err
}
func (c *Cgroup) duplicate() (*os.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.file == nil {
		return nil, errors.New("exec: closed cgroup")
	}
	fd, err := unix.FcntlInt(c.file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "cgroup-run"), nil
}

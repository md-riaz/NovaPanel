// Package backup provides scratch-only foundations for panel self-backup.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// SQLExecutor must execute outside a transaction. A store adapter must retain
// its normal lifetime admission and cancellation until ExecContext returns.
// *sql.DB and *sql.Conn implement this seam; no store internals are exposed.
type SQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Snapshot contains sensitive database bytes, not a completed restic backup.
// The caller must Close it after consuming Path, including on upload failure.
type Snapshot struct {
	dir    *os.File
	parent *os.File
	name   string
	path   string
	info   os.FileInfo
}

// Path returns empty if the private snapshot no longer has its original identity.
// Snapshot methods and consumption require exclusive use until Close.
func (s *Snapshot) Path() string {
	if s.validatePath() != nil {
		return ""
	}
	return s.path
}

func (s *Snapshot) validatePath() error {
	unsafe := errors.New("snapshot path unavailable or changed")
	if s == nil || s.dir == nil || s.info == nil {
		return unsafe
	}
	dir, err := openScratch(filepath.Dir(s.path))
	if err != nil {
		return unsafe
	}
	defer dir.Close()
	actual, err := dir.Stat()
	held, heldErr := s.dir.Stat()
	if err != nil || heldErr != nil || !os.SameFile(actual, held) {
		return unsafe
	}
	fd, err := unix.Openat(int(dir.Fd()), "panel.db", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return unsafe
	}
	file := os.NewFile(uintptr(fd), "panel.db")
	defer file.Close()
	info, err := file.Stat()
	var st unix.Stat_t
	if err != nil || !os.SameFile(info, s.info) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() == 0 || unix.Fstat(fd, &st) != nil || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return unsafe
	}
	return nil
}

// Close removes only this snapshot's private staging directory. It is idempotent.
func (s *Snapshot) Close() error {
	if s.dir == nil {
		return nil
	}
	err := unix.Unlinkat(int(s.dir.Fd()), "panel.db", 0)
	if errors.Is(err, unix.ENOENT) {
		err = nil
	}
	err = errors.Join(err, s.dir.Close())
	err = errors.Join(err, unix.Unlinkat(int(s.parent.Fd()), s.name, unix.AT_REMOVEDIR))
	err = errors.Join(err, s.parent.Sync(), s.parent.Close())
	s.dir, s.parent = nil, nil
	return err
}

// CreateSnapshot uses SQLite VACUUM INTO, including committed WAL content.
// scratch must already be an absolute, same-owner 0700 directory without any
// symlink components. Ancestors must be root/agent-owned and not group/other
// writable, except root-owned sticky directories. It is never an RPC path.
// Root and the agent UID are trusted not to rename or mutate the tree during
// creation or consumption; identity checks do not prevent a trusted-owner race.
// Callers must provide a bounded context and must not mutate this private tree.
func CreateSnapshot(ctx context.Context, source SQLExecutor, scratch string) (_ *Snapshot, err error) {
	if source == nil {
		return nil, errors.New("nil snapshot source")
	}
	parent, err := openScratch(scratch)
	if err != nil {
		return nil, err
	}
	// MkdirTemp supplies an exclusive random name; operate through the held root.
	dirPath, err := os.MkdirTemp(fmt.Sprintf("/proc/self/fd/%d", parent.Fd()), "panel-snapshot-")
	if err != nil {
		parent.Close()
		return nil, err
	}
	name := filepath.Base(dirPath)
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
		parent.Close()
		return nil, err
	}
	s := &Snapshot{dir: os.NewFile(uintptr(fd), name), parent: parent, name: name, path: filepath.Join(scratch, name, "panel.db")}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	// Reserve a previously absent leaf exclusively. SQLite accepts an empty file;
	// this keeps mode 0600 regardless of the process umask, without changing it.
	leaf, err := unix.Openat(fd, "panel.db", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(leaf), "panel.db")
	defer file.Close()
	destination := fmt.Sprintf("/proc/self/fd/%d/panel.db", fd)
	if _, err = source.ExecContext(ctx, "VACUUM main INTO ?", destination); err != nil {
		// SQL/driver error strings may contain source data; do not export them.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("panel snapshot failed")
	}
	s.info, err = file.Stat()
	if err != nil {
		return nil, err
	}
	if err = file.Sync(); err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	if err = s.dir.Sync(); err != nil {
		return nil, err
	}
	if err = parent.Sync(); err != nil {
		return nil, err
	}
	if err = s.validatePath(); err != nil {
		return nil, err
	}
	return s, nil
}

func openScratch(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("invalid scratch path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), "/")
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		var ancestor unix.Stat_t
		if unix.Fstat(int(current.Fd()), &ancestor) != nil ||
			((ancestor.Uid != 0 && ancestor.Uid != uint32(os.Geteuid())) || ancestor.Mode&0022 != 0) &&
				(ancestor.Uid != 0 || ancestor.Mode&unix.S_ISVTX == 0) {
			current.Close()
			return nil, errors.New("unsafe scratch ancestor")
		}
		next, e := unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		current.Close()
		if e != nil {
			return nil, errors.New("unsafe scratch path")
		}
		current = os.NewFile(uintptr(next), part)
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(current.Fd()), &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700 {
		current.Close()
		return nil, errors.New("scratch must be same-owner mode 0700")
	}
	return current, nil
}

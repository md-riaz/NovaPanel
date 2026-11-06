package firewall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	execpkg "novapanel/internal/exec"
)

var errCapture = errors.New("UFW source capture refused")

// UFWSource pins the installed UFW directory. Capture only reads files and uses
// fixed diagnostic commands. This is not wired into public apply or startup.
// Close must not race Capture; root must not mutate source files during capture.
type UFWSource struct {
	root   string
	owner  uint32
	dir    *os.File
	runner execpkg.Runner
}

// OpenUFWSource admits only the production root-owned /etc/ufw layout.
// Tests must inject a fake Runner, since diagnostic commands are host-global.
func OpenUFWSource(runner execpkg.Runner) (*UFWSource, error) {
	return openUFWSource("/etc/ufw", 0, runner)
}
func openUFWSource(root string, owner uint32, runner execpkg.Runner) (*UFWSource, error) {
	if runner == nil {
		return nil, errCapture
	}
	d, e := captureRoot(root, owner)
	if e != nil {
		return nil, errCapture
	}
	return &UFWSource{root, owner, d, runner}, nil
}
func (s *UFWSource) Close() error { return s.dir.Close() }
func captureRoot(root string, owner uint32) (*os.File, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nil, errCapture
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errCapture
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, "/"), "/") {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, errCapture
		}
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || (st.Uid != 0 && st.Uid != owner) || st.Mode&07022 != 0 || st.Mode&0500 != 0500 {
			unix.Close(fd)
			return nil, errCapture
		}
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != owner {
		unix.Close(fd)
		return nil, errCapture
	}
	return os.NewFile(uintptr(fd), root), nil
}
func (s *UFWSource) check(ctx context.Context) error {
	if ctx.Err() != nil {
		return errCapture
	}
	d, e := captureRoot(s.root, s.owner)
	if e != nil {
		return errCapture
	}
	defer d.Close()
	a, e := s.dir.Stat()
	if e != nil {
		return errCapture
	}
	b, e := d.Stat()
	if e != nil || !os.SameFile(a, b) {
		return errCapture
	}
	return nil
}
func (s *UFWSource) file(name string) (*os.File, error) {
	fd, e := unix.Openat(int(s.dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errCapture
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != s.owner || st.Nlink != 1 || st.Mode&07022 != 0 || st.Mode&0400 == 0 {
		unix.Close(fd)
		return nil, errCapture
	}
	return os.NewFile(uintptr(fd), name), nil
}
func (s *UFWSource) command(ctx context.Context, prog string, args ...string) ([]byte, error) {
	if s.check(ctx) != nil {
		return nil, errCapture
	}
	b, _, exit, e := s.runner.Run(ctx, prog, args, execpkg.RunOpts{Env: []string{"LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}, MaxOutput: snapshotFileLimit + 1})
	if e != nil || exit != 0 || len(b) == 0 || len(b) > snapshotFileLimit || s.check(ctx) != nil {
		return nil, errCapture
	}
	return b, nil
}
func (s *UFWSource) Capture(ctx context.Context) (SnapshotInput, error) {
	in := SnapshotInput{Rules: map[string][]byte{}}
	if s.check(ctx) != nil {
		return SnapshotInput{}, errCapture
	}
	held := map[string]*os.File{}
	defer func() {
		for _, f := range held {
			f.Close()
		}
	}()
	for _, n := range snapshotNames[4:] {
		name := strings.TrimPrefix(n, "ufw/")
		f, e := s.file(name)
		if e != nil {
			return SnapshotInput{}, errCapture
		}
		held[name] = f
		b, e := snapshotRead(f, snapshotFileLimit)
		if e != nil {
			return SnapshotInput{}, errCapture
		}
		in.Rules[name] = b
	}
	status, e := s.command(ctx, "/usr/sbin/ufw", "status", "numbered")
	if e != nil {
		return SnapshotInput{}, errCapture
	}
	first, rest, found := strings.Cut(string(status), "\n")
	if !found || strings.Contains(rest, "Status:") {
		return SnapshotInput{}, errCapture
	}
	if first != "Status: active" && first != "Status: inactive" {
		return SnapshotInput{}, errCapture
	}
	in.PriorEnabled = first == "Status: active"
	in.NumberedStatus = status
	in.IPTablesSave, e = s.command(ctx, "/usr/sbin/iptables-save")
	if e != nil {
		return SnapshotInput{}, errCapture
	}
	in.IP6TablesSave, e = s.command(ctx, "/usr/sbin/ip6tables-save")
	if e != nil {
		return SnapshotInput{}, errCapture
	}
	again, e := s.command(ctx, "/usr/sbin/ufw", "status", "numbered")
	if e != nil || !bytes.Equal(status, again) {
		return SnapshotInput{}, errCapture
	}
	for name, f := range held {
		current, e := s.file(name)
		if e != nil {
			return SnapshotInput{}, errCapture
		}
		old, x := f.Stat()
		now, y := current.Stat()
		b, re := snapshotRead(current, snapshotFileLimit)
		ce := current.Close()
		if x != nil || y != nil || !os.SameFile(old, now) || re != nil || ce != nil || !bytes.Equal(b, in.Rules[name]) {
			return SnapshotInput{}, errCapture
		}
	}
	if s.check(ctx) != nil {
		return SnapshotInput{}, errCapture
	}
	// Reuse custody admission to enforce its exact inventory and aggregate bound.
	if _, _, _, e := snapshotInput("00000000000000000000000000", in); e != nil {
		return SnapshotInput{}, errCapture
	}
	return in, nil
}

package store

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"strconv"
	"strings"
)

// The advisory DB lock is authoritative; the pidfile additionally detects live
// owners and is replaced only while that lock is held.
func writePID(path string) error {
	fd, e := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return e
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size > 32 {
		return errors.New("unsafe pidfile")
	}
	raw := make([]byte, st.Size)
	if len(raw) > 0 {
		if _, e = f.Read(raw); e != nil {
			return e
		}
		pid, e := strconv.Atoi(strings.TrimSpace(string(raw)))
		if e != nil || pid <= 0 {
			return errors.New("invalid pidfile")
		}
		e = unix.Kill(pid, 0)
		if e == nil || errors.Is(e, unix.EPERM) {
			return errors.New("pidfile owner still alive")
		}
		if !errors.Is(e, unix.ESRCH) {
			return e
		}
	}
	if e = f.Truncate(0); e != nil {
		return e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	if _, e = fmt.Fprintf(f, "%d\n", os.Getpid()); e != nil {
		return e
	}
	return f.Sync()
}

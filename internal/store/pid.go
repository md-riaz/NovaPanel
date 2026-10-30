package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var bootIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// processStart reads Linux stat field 22. The comm field may contain spaces and ')'.
func processStart(pid int) (string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndex(string(raw), ") ")
	if end < 0 {
		return "", errors.New("invalid process stat")
	}
	fields := strings.Fields(string(raw[end+2:]))
	if len(fields) <= 19 || !validStart(fields[19]) {
		return "", errors.New("invalid process start time")
	}
	return fields[19], nil
}

func validStart(s string) bool {
	n, err := strconv.ParseUint(s, 10, 64)
	return err == nil && strconv.FormatUint(n, 10) == s
}

// The advisory DB lock is authoritative; the pidfile additionally detects live
// owners and is replaced only while that lock is held. New records contain
// "PID boot_id start_ticks\n"; boot_id distinguishes ticks across reboots.
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
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size > 96 {
		return errors.New("unsafe pidfile")
	}
	bootRaw, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return e
	}
	boot := strings.TrimSpace(string(bootRaw))
	if !bootIDPattern.MatchString(boot) {
		return errors.New("invalid boot identity")
	}
	start, e := processStart(os.Getpid())
	if e != nil {
		return e
	}
	raw := make([]byte, st.Size)
	if len(raw) > 0 {
		if _, e = io.ReadFull(f, raw); e != nil {
			return e
		}
		fields := strings.Fields(string(raw))
		if len(fields) != 1 && len(fields) != 3 {
			return errors.New("invalid pidfile")
		}
		pid64, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil || pid64 <= 0 || strconv.FormatInt(pid64, 10) != fields[0] {
			return errors.New("invalid pidfile")
		}
		if len(fields) == 3 && (!bootIDPattern.MatchString(fields[1]) || !validStart(fields[2])) {
			return errors.New("invalid pidfile identity")
		}
		pid := int(pid64)
		e = unix.Kill(pid, 0)
		if e == nil || errors.Is(e, unix.EPERM) {
			// Legacy records lack identity; the held DB lock authorizes recovery.
			if len(fields) == 3 && fields[1] == boot {
				ownerStart, err := processStart(pid)
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				if err == nil && fields[2] == ownerStart {
					return errors.New("pidfile owner still alive")
				}
			}
		} else if !errors.Is(e, unix.ESRCH) {
			return e
		}
	}
	if e = f.Truncate(0); e != nil {
		return e
	}
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	if _, e = fmt.Fprintf(f, "%d %s %s\n", os.Getpid(), boot, start); e != nil {
		return e
	}
	return f.Sync()
}

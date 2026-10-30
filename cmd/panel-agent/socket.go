package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// reserveSocket runs only after OpenState owns the DB lifetime lock and validates
// root-controlled parent directories. Its lock also excludes different DB paths.
func reserveSocket(path string) (func() error, error) {
	fd, e := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path+".lock")
	fail := func(e error) (func() error, error) { f.Close(); return nil, e }
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return fail(e)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return fail(errors.New("unsafe socket lock"))
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return fail(fmt.Errorf("socket already reserved: %w", e))
	}
	return f.Close, nil
}

func recoverSocket(path string) error {
	// Pin the validated directory and recheck identity after the liveness probe.
	fd, e := unix.Open(filepath.Dir(path), unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	name := filepath.Base(path)
	var before, after unix.Stat_t
	if e = unix.Fstatat(fd, name, &before, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		if errors.Is(e, unix.ENOENT) {
			return nil
		}
		return e
	}
	if before.Mode&unix.S_IFMT != unix.S_IFSOCK || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 {
		return errors.New("unsafe existing socket path")
	}
	c, e := net.DialTimeout("unix", path, 100*time.Millisecond)
	if e == nil {
		c.Close()
		return errors.New("socket already listening")
	}
	if !errors.Is(e, unix.ECONNREFUSED) {
		return fmt.Errorf("cannot establish stale socket: %w", e)
	}
	if e = unix.Fstatat(fd, name, &after, unix.AT_SYMLINK_NOFOLLOW); e != nil {
		return e
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid {
		return errors.New("socket path changed during recovery")
	}
	return unix.Unlinkat(fd, name, 0)
}

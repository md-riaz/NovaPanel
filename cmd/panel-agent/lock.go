package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

// The persistent lock inode must never be unlinked on exit. Removing it would
// let a waiter lock an old inode while a third process creates a new one.
func lockInstance(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	fail := func(e error) (*os.File, error) { f.Close(); return nil, e }
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return fail(fmt.Errorf("unsafe pidfile"))
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fail(fmt.Errorf("agent already running: %w", err))
	}
	if err = f.Truncate(0); err != nil {
		return fail(err)
	}
	if _, err = fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		return fail(err)
	}
	if err = f.Sync(); err != nil {
		return fail(err)
	}
	return f, nil
}

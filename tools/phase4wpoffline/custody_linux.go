//go:build wordpress_offline_lab && linux

package main

import (
	"crypto/sha256"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// labConfig verifies custody before PHP can interpret the ini at its fixed path.
// Root is trusted to exclude replacement while the lab runs.
func labConfig(dir string) error {
	d, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errRuntimeRefused
	}
	defer unix.Close(d)
	var ds unix.Stat_t
	if unix.Fstat(d, &ds) != nil || ds.Uid != 0 || ds.Mode&0022 != 0 {
		return errRuntimeRefused
	}
	f, err := unix.Openat(d, "php.ini", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errRuntimeRefused
	}
	file := os.NewFile(uintptr(f), "php.ini")
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(f, &st) != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0022 != 0 || st.Nlink != 1 || st.Size > 4096 {
		return errRuntimeRefused
	}
	b, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || sha256.Sum256(b) != [32]byte{0x42, 0x09, 0xf7, 0xa9, 0x22, 0xbc, 0xc9, 0xfe, 0x45, 0xf4, 0x50, 0x83, 0x45, 0xfd, 0x61, 0x31, 0x3f, 0x4c, 0xe1, 0x5e, 0x49, 0x6c, 0x6f, 0x85, 0x1a, 0x0d, 0x69, 0xae, 0xae, 0x94, 0x2b, 0x42} {
		return errRuntimeRefused
	}
	return nil
}

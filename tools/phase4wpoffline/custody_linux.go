//go:build wordpress_offline_lab && linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// lockLab coordinates with trusted maintenance tooling; raw root can bypass it.
func lockLab(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errRuntimeRefused
	}
	f := os.NewFile(uintptr(fd), "lab maintenance lock")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		f.Close()
		return nil, errRuntimeRefused
	}
	return f, nil
}

// labConfig verifies custody before PHP can interpret the ini at its fixed path.
// Root is trusted to exclude replacement while the lab runs.
func labConfig(dir string) error {
	return checkLabFiles(dir, map[string]string{"php.ini": "4209f7a922bcc9fe45f4508345fd61313f4ce15e496c6f851a0d69aeae942b42"})
}

func labArtifacts(dir string) error {
	return checkLabFiles(dir, map[string]string{
		"bootstrap.php":  "eafb987a50715504cb89e845e3bc31f296a8e367d5d2ae6c03ca027dd20108cc",
		"transport.php":  "57b4a7e536323aad601df40bee0ef356c3e2208f12d7292c5a57bbc8a449e91b",
		"cli.phar":       "ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c",
		"checksums.json": "bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7",
	})
}

func checkLabFiles(dir string, files map[string]string) error {
	d, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errRuntimeRefused
	}
	defer unix.Close(d)
	var ds unix.Stat_t
	if unix.Fstat(d, &ds) != nil || ds.Uid != 0 || ds.Mode&0022 != 0 {
		return errRuntimeRefused
	}
	for name, want := range files {
		if err := checkLabFile(d, name, want); err != nil {
			return err
		}
	}
	return nil
}

func checkLabFile(d int, name, want string) error {
	f, err := unix.Openat(d, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return errRuntimeRefused
	}
	file := os.NewFile(uintptr(f), name)
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(f, &st) != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0022 != 0 || st.Nlink != 1 || st.Size <= 0 || st.Size > 8<<20 {
		return errRuntimeRefused
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(file, (8<<20)+1)); err != nil || hex.EncodeToString(h.Sum(nil)) != want {
		return errRuntimeRefused
	}
	return nil
}

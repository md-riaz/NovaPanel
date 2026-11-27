package wordpress

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"novapanel/internal/validate"
)

// EnsureToolWorkspace is called while the fresh account lifetime and locks are held.
func EnsureToolWorkspace(sites, name string, uid, gid uint32) error {
	if !filepath.IsAbs(sites) || filepath.Clean(sites) != sites || (&validate.V{}).SiteName(name) != nil || uid == 0 || gid == 0 {
		return errStaged
	}
	resolved, err := filepath.EvalSymlinks(sites)
	if err != nil || resolved != sites {
		return errStaged
	}
	root, err := unix.Open(sites, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errStaged
	}
	defer unix.Close(root)
	home, err := unix.Openat(root, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errStaged
	}
	defer unix.Close(home)
	var st unix.Stat_t
	if unix.Fstat(home, &st) != nil || st.Uid != uid || st.Gid != gid || st.Mode&07777 != 0751 {
		return errStaged
	}
	open := func(parent int, name string) (int, error) {
		if err := unix.Mkdirat(parent, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, errStaged
		}
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return -1, errStaged
		}
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&07777 != 0700 {
			unix.Close(fd)
			return -1, errStaged
		}
		if st.Uid == 0 && st.Gid == 0 {
			dup, err := unix.Dup(fd)
			if err != nil {
				unix.Close(fd)
				return -1, errStaged
			}
			f := os.NewFile(uintptr(dup), name)
			_, err = f.Readdirnames(1)
			f.Close()
			if !errors.Is(err, io.EOF) || unix.Fchown(fd, int(uid), int(gid)) != nil {
				unix.Close(fd)
				return -1, errStaged
			}
		} else if st.Uid != uid || st.Gid != gid {
			unix.Close(fd)
			return -1, errStaged
		}
		return fd, nil
	}
	base, err := open(home, ".novapanel-wpcli")
	if err != nil {
		return err
	}
	defer unix.Close(base)
	for _, name := range []string{"home", "cache", "packages"} {
		fd, err := open(base, name)
		if err != nil {
			return err
		}
		unix.Close(fd)
	}
	return nil
}

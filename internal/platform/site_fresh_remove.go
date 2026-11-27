package platform

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"novapanel/internal/validate"
)

// RemoveFreshSitePaths removes only same-device trees owned by the reserved UID.
// The caller has retired the account and excluded every site-UID process.
func RemoveFreshSitePaths(sites, logs, name, siteID, jobID string, uid uint32) error {
	if uid < 2000000 || uid > 2999999 || (&validate.V{}).SiteName(name) != nil {
		return errDomain
	}
	if err := VerifyFreshSitePaths(sites, logs, name, siteID, jobID, uid); err != nil {
		return err
	}
	for _, path := range []string{sites, logs} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(path) || resolved != path {
			return errDomain
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			return err
		}
		err = removeFreshOwnedTree(root, name, siteID, jobID, uid)
		closeErr := root.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// VerifyFreshSitePaths proves both trees before any destructive account effect.
func VerifyFreshSitePaths(sites, logs, name, siteID, jobID string, uid uint32) error {
	if uid < 2000000 || uid > 2999999 || (&validate.V{}).SiteName(name) != nil {
		return errDomain
	}
	for _, path := range []string{sites, logs} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(path) || resolved != path {
			return errDomain
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			return err
		}
		err = verifyFreshOwnedTree(root, name, siteID, jobID, uid)
		closeErr := root.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func verifyFreshOwnedTree(root *os.Root, name, siteID, jobID string, uid uint32) error {
	marked, err := checkFreshMarker(root, name, siteID, jobID, uid)
	if err != nil {
		return err
	}
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !marked || !info.IsDir() {
		return errDomain
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	var base unix.Stat_t
	if unix.Fstat(int(parent.Fd()), &base) != nil {
		return errDomain
	}
	fd, err := unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV})
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), "fresh-site-verify")
	defer dir.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uid || st.Dev != base.Dev {
		return errDomain
	}
	return checkFreshTree(dir, base.Dev)
}

func removeFreshOwnedTree(root *os.Root, name, siteID, jobID string, uid uint32) error {
	marked, err := checkFreshMarker(root, name, siteID, jobID, uid)
	if err != nil {
		return err
	}
	if !marked {
		if _, err := root.Lstat(name); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return errDomain
	}

	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	var base unix.Stat_t
	if err = unix.Fstat(int(parent.Fd()), &base); err != nil {
		return err
	}
	fd, err := unix.Openat2(int(parent.Fd()), name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV})
	if errors.Is(err, unix.ENOENT) {
		if err = root.Remove(freshMarkerName(siteID)); err != nil {
			return err
		}
		return parent.Sync()
	}
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), "fresh-site")
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err == nil && (st.Uid != uid || st.Dev != base.Dev) {
		err = errDomain
	}
	if err == nil {
		err = checkFreshTree(dir, base.Dev)
	}
	if e := dir.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = root.RemoveAll(name); err != nil {
		return err
	}
	if err = root.Remove(freshMarkerName(siteID)); err != nil {
		return err
	}
	return parent.Sync()
}

func checkFreshTree(dir *os.File, device uint64) error {
	for {
		names, err := dir.Readdirnames(128)
		for _, name := range names {
			fd, e := unix.Openat2(int(dir.Fd()), name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV})
			if e != nil {
				return e
			}
			var st unix.Stat_t
			e = unix.Fstat(fd, &st)
			unix.Close(fd)
			if e != nil {
				return e
			}
			if st.Dev != device {
				return errDomain
			}
			if st.Mode&unix.S_IFMT == unix.S_IFDIR {
				fd, e = unix.Openat2(int(dir.Fd()), name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_XDEV})
				if e != nil {
					return e
				}
				child := os.NewFile(uintptr(fd), "fresh-child")
				e = checkFreshTree(child, device)
				if closeErr := child.Close(); e == nil {
					e = closeErr
				}
				if e != nil {
					return e
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

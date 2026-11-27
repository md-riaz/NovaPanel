package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
)

// The marker lives in the root-owned parent, outside site-UID write authority.
func freshMarkerName(siteID string) string { return ".novapanel-owner-" + siteID }
func freshMarkerContents(name, siteID, jobID string, uid uint32) string {
	return name + "\n" + siteID + "\n" + jobID + "\n" + strconv.FormatUint(uint64(uid), 10) + "\n"
}

func checkFreshMarker(root *os.Root, name, siteID, jobID string, uid uint32) (bool, error) {
	if !id.Valid(siteID) || !id.Valid(jobID) {
		return false, errDomain
	}
	parent, err := root.Open(".")
	if err != nil {
		return false, err
	}
	defer parent.Close()
	var ps unix.Stat_t
	if unix.Fstat(int(parent.Fd()), &ps) != nil || ps.Uid != 0 || ps.Mode&0022 != 0 {
		return false, errDomain
	}
	fd, err := unix.Openat(int(parent.Fd()), freshMarkerName(siteID), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, errDomain
	}
	f := os.NewFile(uintptr(fd), "fresh-owner")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Nlink != 1 || st.Mode&07777 != 0600 || st.Size > 512 {
		return false, errDomain
	}
	raw, err := io.ReadAll(io.LimitReader(f, 513))
	if err != nil || string(raw) != freshMarkerContents(name, siteID, jobID, uid) {
		return false, errDomain
	}
	return true, nil
}

// The root-owned write-ahead marker is durable before creating either tree.
func prepareFreshMarker(root *os.Root, name, siteID, jobID string, uid uint32) error {
	exists, err := checkFreshMarker(root, name, siteID, jobID, uid)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err = root.Lstat(name); err == nil {
		return fmt.Errorf("%w: unowned site tree", errDomain)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	tmp := freshMarkerName(siteID) + ".tmp"
	fd, err := unix.Openat(int(parent.Fd()), tmp, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "fresh-owner-temp")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Nlink != 1 || st.Mode&07777 != 0600 {
		f.Close()
		return errDomain
	}
	if err = f.Truncate(0); err == nil {
		_, err = f.WriteString(freshMarkerContents(name, siteID, jobID, uid))
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = unix.Renameat2(int(parent.Fd()), tmp, int(parent.Fd()), freshMarkerName(siteID), unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return parent.Sync()
}

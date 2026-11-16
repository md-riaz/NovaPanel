//go:build wordpress_offline_lab && linux

package offline

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const wpURL = "https://downloads.wordpress.org/release/wordpress-7.1.4.zip"

var errRefused = errors.New("offline: restricted backend refused")

// ProtectedLifetime is the opaque capability the backend REQUIRES before any
// write. In production only the (deferred) operation-scoped lifetime owner can
// mint it; this whole package is lab-only (wordpress_offline_lab), so the
// constructor here is reachable only in tagged lab/test builds.
type ProtectedLifetime struct {
	uid, gid          uint32
	name, id, docroot string
	established       bool
}

// NewLabLifetime mints a lab/test-only established handle. Never reachable in a
// production build because of the wordpress_offline_lab tag.
func NewLabLifetime(uid, gid uint32, name, id, docroot string) ProtectedLifetime {
	return ProtectedLifetime{uid: uid, gid: gid, name: name, id: id, docroot: docroot, established: true}
}

// InstallWordPress materializes the pinned, digest-verified WordPress payload
// into the site-UID-owned docroot and atomically publishes wp-config.php as a
// site-owned 0400 regular file. It never execs, follows no symlink, stays on a
// single device, and refuses entirely without a valid established handle.
func InstallWordPress(lt ProtectedLifetime, wpConfig []byte) error {
	// 1. If !lt.established -> return errRefused. (fail-closed: no handle, no write.)
	if !lt.established {
		return errRefused
	}

	// 2. If lt.uid==0 || lt.gid==0 || len(wpConfig)==0 -> errRefused. Validate docroot.
	if lt.uid == 0 || lt.gid == 0 || len(wpConfig) == 0 {
		return errRefused
	}
	if !filepath.IsAbs(lt.docroot) || filepath.Clean(lt.docroot) != lt.docroot {
		return errRefused
	}

	// 3. Open the docroot no-follow directory fd.
	dfd, err := unix.Open(lt.docroot, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errRefused
	}
	dir := os.NewFile(uintptr(dfd), lt.docroot)
	defer dir.Close()

	// 4. Fstat the fd; require dir, uid match; record dev.
	var ds unix.Stat_t
	if unix.Fstat(dfd, &ds) != nil || ds.Mode&unix.S_IFMT != unix.S_IFDIR || ds.Uid != lt.uid {
		return errRefused
	}
	dev := ds.Dev

	// 5. Refuse a nonempty docroot.
	names, err := dir.Readdirnames(-1)
	if err != nil || len(names) != 0 {
		return errRefused
	}

	// 6. Get payload and open zip.
	wp, err := Resolve(wpURL)
	if err != nil {
		return errRefused
	}
	zr, err := zip.NewReader(bytes.NewReader(wp), int64(len(wp)))
	if err != nil {
		return errRefused
	}

	// 7. Extract.
	if err := extract(dfd, zr, lt.uid, lt.gid, dev); err != nil {
		return err
	}

	// 8. Publish config.
	if err := publishConfig(dfd, lt.uid, lt.gid, wpConfig); err != nil {
		return err
	}

	// 9. Success.
	return nil
}

// extract unpacks the WordPress ZIP to the docroot.
func extract(dfd int, zr *zip.Reader, uid, gid uint32, dev uint64) error {
	for _, zf := range zr.File {
		name := zf.Name

		// Reject empty names, backslashes, absolute paths.
		if name == "" || strings.Contains(name, "\\") || path.IsAbs(name) || strings.HasPrefix(name, "/") {
			return errRefused
		}

		// Require "wordpress/" prefix.
		if !strings.HasPrefix(name, "wordpress/") {
			return errRefused
		}
		rel := strings.TrimPrefix(name, "wordpress/")

		// Skip the top-level directory entry itself.
		if rel == "" {
			continue
		}

		// Clean-and-escape check.
		clean := path.Clean(rel)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return errRefused
		}
		// Reject trailing slash inconsistency.
		if clean != strings.TrimRight(rel, "/") {
			return errRefused
		}
		// Reject unsafe components: empty, ".", ".." (split the cleaned path so a
		// directory entry's trailing slash does not yield an empty component).
		components := strings.Split(clean, "/")
		for _, comp := range components {
			if comp == "" || comp == "." || comp == ".." {
				return errRefused
			}
		}

		info := zf.FileInfo()
		mode := info.Mode()

		if mode.IsDir() {
			// Create directory chain.
			if err := mkdirChain(dfd, clean, uid, gid); err != nil {
				return err
			}
		} else if mode.IsRegular() {
			// Ensure parent dirs, then write file.
			parentDir := path.Dir(clean)
			if parentDir != "." && parentDir != "" {
				if err := mkdirChain(dfd, parentDir, uid, gid); err != nil {
					return err
				}
			}
			if err := writeFile(dfd, clean, uid, gid, dev, zf); err != nil {
				return err
			}
		} else {
			// Reject symlinks and other irregular entries.
			return errRefused
		}
	}
	return nil
}

// mkdirChain creates each component of rel under dfd, descending only through
// no-follow directory opens so a symlink component can never be traversed, and
// setting owner and mode on the opened descriptor (robust across kernels,
// unlike fchmodat with AT_SYMLINK_NOFOLLOW).
func mkdirChain(dfd int, rel string, uid, gid uint32) error {
	if rel == "." || rel == "" {
		return nil
	}
	cur := dfd
	closeCur := func() {
		if cur != dfd {
			unix.Close(cur)
		}
	}
	for _, comp := range strings.Split(rel, "/") {
		if comp == "" || comp == "." || comp == ".." {
			closeCur()
			return errRefused
		}
		if err := unix.Mkdirat(cur, comp, 0750); err != nil && err != unix.EEXIST {
			closeCur()
			return errRefused
		}
		// O_NOFOLLOW|O_DIRECTORY rejects a symlink or non-directory component.
		nfd, err := unix.Openat(cur, comp, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		closeCur()
		if err != nil {
			return errRefused
		}
		var st unix.Stat_t
		if unix.Fstat(nfd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
			unix.Close(nfd)
			return errRefused
		}
		if unix.Fchown(nfd, int(uid), int(gid)) != nil || unix.Fchmod(nfd, 0750) != nil {
			unix.Close(nfd)
			return errRefused
		}
		cur = nfd
	}
	if cur != dfd {
		unix.Close(cur)
	}
	return nil
}

// openDir opens an existing directory path relative to dfd, no-follow, without
// creating anything. The returned fd is dfd itself for "." or "" and must not
// be closed by the caller in that case.
func openDir(dfd int, rel string) (int, error) {
	if rel == "." || rel == "" {
		return dfd, nil
	}
	cur := dfd
	for _, comp := range strings.Split(rel, "/") {
		nfd, err := unix.Openat(cur, comp, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if cur != dfd {
			unix.Close(cur)
		}
		if err != nil {
			return -1, errRefused
		}
		cur = nfd
	}
	return cur, nil
}

// writeFile extracts a single file entry to the docroot.
func writeFile(dfd int, rel string, uid, gid uint32, dev uint64, zf *zip.File) error {
	// Split into dir and base.
	dir := path.Dir(rel)
	base := path.Base(rel)

	// Open parent directory.
	var parent int
	if dir == "." {
		parent = dfd
	} else {
		var err error
		parent, err = openDir(dfd, dir)
		if err != nil {
			return err
		}
	}
	defer func() {
		if parent != dfd {
			unix.Close(parent)
		}
	}()

	// Create file.
	ffd, err := unix.Openat(parent, base, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0640)
	if err != nil {
		return errRefused
	}
	f := os.NewFile(uintptr(ffd), base)

	// Fstat: must be regular and same device.
	var fst unix.Stat_t
	if unix.Fstat(ffd, &fst) != nil || fst.Mode&unix.S_IFMT != unix.S_IFREG || fst.Dev != dev {
		f.Close()
		return errRefused
	}

	// Copy contents.
	rc, err := zf.Open()
	if err != nil {
		f.Close()
		return errRefused
	}
	const maxFileBytes = 64 << 20 // 64 MiB
	_, err = io.Copy(f, io.LimitReader(rc, maxFileBytes))
	rc.Close()
	if err != nil {
		f.Close()
		return errRefused
	}

	// Chown and chmod.
	if err := unix.Fchown(ffd, int(uid), int(gid)); err != nil {
		f.Close()
		return errRefused
	}
	if err := unix.Fchmod(ffd, 0640); err != nil {
		f.Close()
		return errRefused
	}

	f.Close()
	return nil
}

// publishConfig writes wp-config.php atomically.
func publishConfig(dfd int, uid, gid uint32, content []byte) error {
	// Write to temp file.
	tmp := "wp-config.php.tmp"
	tfd, err := unix.Openat(dfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0400)
	if err != nil {
		return errRefused
	}
	tf := os.NewFile(uintptr(tfd), tmp)

	// Write content.
	if _, err := tf.Write(content); err != nil {
		tf.Close()
		return errRefused
	}

	// Chown and chmod.
	if err := unix.Fchown(tfd, int(uid), int(gid)); err != nil {
		tf.Close()
		return errRefused
	}
	if err := unix.Fchmod(tfd, 0400); err != nil {
		tf.Close()
		return errRefused
	}

	tf.Close()

	// Rename to final name.
	if err := unix.Renameat(dfd, tmp, dfd, "wp-config.php"); err != nil {
		return errRefused
	}

	return nil
}

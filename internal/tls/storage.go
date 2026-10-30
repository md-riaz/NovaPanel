package tls

import (
	"errors"
	"golang.org/x/sys/unix"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
)

// Install publishes both files through one relative symlink swap. It does not
// reload nginx or discard older versions needed for apply/probe recovery.
func Install(root, primary string, c *Certificate) (string, error) {
	if c == nil || !c.Covers(primary) || len(c.certPEM) == 0 || len(c.keyPEM) == 0 {
		return "", invalid()
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", invalid()
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return "", invalid()
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return "", storageError()
	}
	defer base.Close()
	if err = safeDir(base, "."); err != nil {
		return "", err
	}
	if err = base.Mkdir(primary, 0700); err != nil && !os.IsExist(err) {
		return "", storageError()
	}
	if err = safeDir(base, primary); err != nil {
		return "", err
	}
	site, err := base.OpenRoot(primary)
	if err != nil {
		return "", storageError()
	}
	defer site.Close()
	if st, e := site.Lstat("current"); e == nil {
		if st.Mode()&os.ModeSymlink == 0 {
			return "", invalid()
		}
		target, e := site.Readlink("current")
		if e != nil || !id.Valid(target) {
			return "", invalid()
		}
		if e = safeDir(site, target); e != nil {
			return "", e
		}
	} else if !os.IsNotExist(e) {
		return "", storageError()
	}
	version, err := id.New()
	if err != nil {
		return "", storageError()
	}
	if err = site.Mkdir(version, 0700); err != nil {
		return "", storageError()
	}
	published := false
	defer func() {
		if !published {
			_ = site.RemoveAll(version)
		}
	}()
	dir, err := site.OpenRoot(version)
	if err != nil {
		return "", storageError()
	}
	defer dir.Close()
	for _, file := range []struct {
		name string
		data []byte
	}{{"fullchain.pem", c.certPEM}, {"privkey.pem", c.keyPEM}} {
		f, e := dir.OpenFile(file.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
		if e != nil {
			return "", storageError()
		}
		_, e = f.Write(file.data)
		if e == nil {
			e = f.Sync()
		}
		e = errors.Join(e, f.Close())
		if e != nil {
			return "", storageError()
		}
	}
	if err = syncDir(dir); err != nil {
		return "", err
	}
	temp := ".current-" + version
	if err = site.Symlink(version, temp); err != nil {
		return "", storageError()
	}
	defer site.Remove(temp)
	if err = site.Rename(temp, "current"); err != nil {
		return "", storageError()
	}
	published = true
	if err = syncDir(site); err != nil {
		return "", err
	}
	if err = syncDir(base); err != nil {
		return "", err
	}
	return version, nil
}
func storageError() error { return rpcerr.Errf("agent_error", "TLS storage operation failed") }
func safeDir(root *os.Root, name string) error {
	st, err := root.Lstat(name)
	if err != nil {
		return storageError()
	}
	// Check ownership through the opened directory descriptor.
	if !st.IsDir() || st.Mode().Perm() != 0700 || st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return invalid()
	}
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return storageError()
	}
	defer f.Close()
	var s unix.Stat_t
	if unix.Fstat(int(f.Fd()), &s) != nil || s.Uid != uint32(os.Geteuid()) {
		return invalid()
	}
	return nil
}
func syncDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return storageError()
	}
	defer f.Close()
	if f.Sync() != nil {
		return storageError()
	}
	return nil
}

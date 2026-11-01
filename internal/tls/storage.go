package tls

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
)

// Install stages and immediately publishes a pair. Use Stage/Publish/Restore
// when apply or probe failure must restore the previous pointer.
func Install(root, primary string, c *Certificate) (string, error) {
	token, err := Stage(root, primary, c)
	if err != nil {
		return "", err
	}
	if err := Publish(token); err != nil {
		return "", err
	}
	return token.Version, nil
}

// Stage syncs a complete pair without changing current or deleting older pairs.
func Stage(root, primary string, c *Certificate) (Recovery, error) {
	if c == nil || !c.Covers(primary) || len(c.certPEM) == 0 || len(c.keyPEM) == 0 {
		return Recovery{}, invalid()
	}
	site, base, unlock, err := openPublication(root, primary, true)
	if err != nil {
		return Recovery{}, err
	}
	defer base.Close()
	defer site.Close()
	defer unlock()
	previous, err := currentVersion(site)
	if err != nil {
		return Recovery{}, err
	}
	if previous != "" {
		if err := checkPairFiles(site, previous); err != nil {
			return Recovery{}, err
		}
	}
	version, err := id.New()
	if err != nil {
		return Recovery{}, storageError()
	}
	if err = site.Mkdir(version, 0700); err != nil {
		return Recovery{}, storageError()
	}
	staged := false
	defer func() {
		if !staged {
			_ = site.RemoveAll(version)
		}
	}()
	dir, err := site.OpenRoot(version)
	if err != nil {
		return Recovery{}, storageError()
	}
	defer dir.Close()
	for _, file := range []struct {
		name string
		data []byte
	}{{"fullchain.pem", c.certPEM}, {"privkey.pem", c.keyPEM}} {
		f, e := dir.OpenFile(file.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
		if e != nil {
			return Recovery{}, storageError()
		}
		_, e = f.Write(file.data)
		if e == nil {
			e = f.Sync()
		}
		e = errors.Join(e, f.Close())
		if e != nil {
			return Recovery{}, storageError()
		}
	}
	if err = syncDir(dir); err != nil {
		return Recovery{}, err
	}
	if err = syncDir(site); err != nil {
		return Recovery{}, err
	}
	if err = syncDir(base); err != nil {
		return Recovery{}, err
	}
	staged = true
	return Recovery{Root: root, Primary: primary, Version: version, Previous: previous}, nil
}

func storageError() error { return rpcerr.Errf("agent_error", "TLS storage operation failed") }
func safeDir(root *os.Root, name string) error {
	st, err := root.Lstat(name)
	if err != nil {
		return storageError()
	}
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

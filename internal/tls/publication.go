package tls

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
)

// Recovery contains only relative version identifiers and storage coordinates,
// never certificate or key bytes. Previous is empty for the first installation.
// A trusted caller may persist it before Publish, then Restore after a failed
// apply/probe. Do not accept storage coordinates from an untrusted RPC caller.
// This is not a durable apply journal and does not track nginx or database state.
type Recovery struct {
	Root     string `json:"root"`
	Primary  string `json:"primary"`
	Version  string `json:"version"`
	Previous string `json:"previous"`
}

// Publish atomically selects the staged pair if current still matches Previous.
// Repeating the same publication is a no-op. Callers must serialize the entire
// publish/apply/probe/restore sequence, not just individual pointer operations.
// On a post-rename sync error the pointer may have changed; retain the token.
func Publish(token Recovery) error { return changePointer(token, false) }

// Restore returns current to Previous, or removes it on first installation.
// A newer current pointer is a conflict and is never overwritten. Both version
// directories are retained. Repeating a completed restore is a no-op.
func Restore(token Recovery) error { return changePointer(token, true) }

func changePointer(token Recovery, restore bool) error {
	if !id.Valid(token.Version) || (token.Previous != "" && !id.Valid(token.Previous)) || token.Version == token.Previous {
		return invalid()
	}
	site, base, unlock, err := openPublication(token.Root, token.Primary, false)
	if err != nil {
		return err
	}
	defer base.Close()
	defer site.Close()
	defer unlock()
	current, err := currentVersion(site)
	if err != nil {
		return err
	}
	expected, target := token.Previous, token.Version
	if restore {
		expected, target = token.Version, token.Previous
	}
	if current != expected && current != target {
		return rpcerr.Errf("conflict", "TLS current version changed")
	}
	// Validate both retained pairs before selecting either pointer.
	for _, version := range []string{token.Version, token.Previous} {
		if version == "" {
			continue
		}
		if err := checkPairFiles(site, version); err != nil {
			return err
		}
	}
	if current != target {
		if target == "" {
			if err := site.Remove("current"); err != nil {
				return storageError()
			}
		} else {
			unique, err := id.New()
			if err != nil {
				return storageError()
			}
			temp := ".current-" + unique
			if err := site.Symlink(target, temp); err != nil {
				return storageError()
			}
			defer site.Remove(temp)
			if err := site.Rename(temp, "current"); err != nil {
				return storageError()
			}
		}
	}
	return syncDir(site)
}

func checkPairFiles(site *os.Root, version string) error {
	if err := safeDir(site, version); err != nil {
		return err
	}
	dir, err := site.OpenRoot(version)
	if err != nil {
		return storageError()
	}
	defer dir.Close()
	for _, file := range []struct {
		name  string
		limit int64
	}{{"fullchain.pem", MaxCertificateBytes}, {"privkey.pem", MaxKeyBytes}} {
		data, err := readPairFile(dir, file.name, file.limit)
		clear(data)
		if err != nil {
			return err
		}
	}
	return nil
}

func currentVersion(site *os.Root) (string, error) {
	st, err := site.Lstat("current")
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", storageError()
	}
	if st.Mode()&os.ModeSymlink == 0 {
		return "", invalid()
	}
	version, err := site.Readlink("current")
	if err != nil || !id.Valid(version) {
		return "", invalid()
	}
	if err := safeDir(site, version); err != nil {
		return "", err
	}
	return version, nil
}

// Lock the held site directory, avoiding lockfiles and serializing cooperating
// callers across processes. Owner-controlled ancestors must remain stable.
func openPublication(root, primary string, create bool) (*os.Root, *os.Root, func(), error) {
	if !validDomain(primary) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, nil, nil, invalid()
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, nil, nil, invalid()
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, nil, storageError()
	}
	fail := func(err error) (*os.Root, *os.Root, func(), error) { base.Close(); return nil, nil, nil, err }
	if err := safeDir(base, "."); err != nil {
		return fail(err)
	}
	if create {
		if err := base.Mkdir(primary, 0700); err != nil && !os.IsExist(err) {
			return fail(storageError())
		}
	}
	if err := safeDir(base, primary); err != nil {
		return fail(err)
	}
	site, err := base.OpenRoot(primary)
	if err != nil {
		return fail(storageError())
	}
	f, err := site.OpenFile(".", os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		site.Close()
		return fail(storageError())
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		site.Close()
		return fail(storageError())
	}
	return site, base, func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
}

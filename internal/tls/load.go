package tls

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/id"
)

// Load reads one installed version and validates its pair against current domains
// and time. A pointer swap cannot mix certificate and key from different versions.
func Load(root, primary string, domains []string, now time.Time) (*Certificate, error) {
	if !validDomain(primary) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, invalid()
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return nil, invalid()
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, storageError()
	}
	defer base.Close()
	if err := safeDir(base, "."); err != nil {
		return nil, err
	}
	if err := safeDir(base, primary); err != nil {
		return nil, err
	}
	site, err := base.OpenRoot(primary)
	if err != nil {
		return nil, storageError()
	}
	defer site.Close()
	version, err := site.Readlink("current")
	if err != nil || !id.Valid(version) {
		return nil, invalid()
	}
	if err := safeDir(site, version); err != nil {
		return nil, err
	}
	dir, err := site.OpenRoot(version)
	if err != nil {
		return nil, storageError()
	}
	defer dir.Close()
	cert, err := readPairFile(dir, "fullchain.pem", MaxCertificateBytes)
	if err != nil {
		return nil, err
	}
	key, err := readPairFile(dir, "privkey.pem", MaxKeyBytes)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return Validate(cert, key, domains, now)
}

func readPairFile(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, storageError()
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, storageError()
	}
	var raw unix.Stat_t
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || st.Size() <= 0 || st.Size() > limit || unix.Fstat(int(f.Fd()), &raw) != nil || raw.Uid != uint32(os.Geteuid()) {
		return nil, invalid()
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) == 0 || int64(len(data)) > limit {
		clear(data)
		return nil, invalid()
	}
	return data, nil
}

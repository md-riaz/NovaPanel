package nginx

import (
	"crypto/x509"
	"encoding/pem"
	"golang.org/x/sys/unix"
	"io"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"os"
	"path/filepath"
	"time"

	certstore "novapanel/internal/tls"
)

// TLSState is an immutable snapshot of a validated installed pair. Only LoadTLS
// can construct a usable value; staging is trusted agent-owned provenance.
type TLSState struct {
	root, primary string
	staging       bool
	leaf          *x509.Certificate
	certificate   *certstore.Certificate
}

// LoadTLS validates the installed pair and primary SAN. Additional aliases are
// partitioned by actual SAN coverage, not by persisted cert_state labels.
func LoadTLS(root, primary string, staging bool, now time.Time) (*TLSState, error) {
	// Pin current before validation so the identity read cannot follow a later swap.
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	version, err := base.Readlink(filepath.Join(primary, "current"))
	if err != nil || !id.Valid(version) {
		return nil, rpcerr.Errf("invalid_argument", "Invalid installed TLS version")
	}
	if _, err := certstore.Load(root, primary, []string{primary}, now); err != nil {
		return nil, err
	}
	info, err := base.Lstat(filepath.Join(primary, version))
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, rpcerr.Errf("invalid_argument", "Invalid installed TLS directory")
	}
	versionDir, err := base.OpenFile(filepath.Join(primary, version), os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer versionDir.Close()
	var owner unix.Stat_t
	if unix.Fstat(int(versionDir.Fd()), &owner) != nil || owner.Uid != uint32(os.Geteuid()) {
		return nil, rpcerr.Errf("invalid_argument", "Invalid installed TLS directory")
	}
	dir, err := base.OpenRoot(filepath.Join(primary, version))
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	certPEM, err := readInstalledPairFile(dir, "fullchain.pem", certstore.MaxCertificateBytes)
	if err != nil {
		return nil, err
	}
	keyPEM, err := readInstalledPairFile(dir, "privkey.pem", certstore.MaxKeyBytes)
	if err != nil {
		return nil, err
	}
	defer clear(keyPEM)
	// Validate the pinned pair too: current may have changed during the first load.
	c, err := certstore.Validate(certPEM, keyPEM, []string{primary}, now)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &TLSState{root: root, primary: primary, staging: staging, certificate: c, leaf: leaf}, nil
}

func readInstalledPairFile(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var raw unix.Stat_t
	if !info.Mode().IsRegular() || info.Mode() != 0600 || info.Size() <= 0 || info.Size() > limit || unix.Fstat(int(f.Fd()), &raw) != nil || raw.Uid != uint32(os.Geteuid()) {
		return nil, rpcerr.Errf("invalid_argument", "Invalid installed TLS file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		clear(data)
		return nil, rpcerr.Errf("invalid_argument", "Invalid installed TLS file")
	}
	return data, nil
}

func (s *TLSState) covers(host string) bool {
	return s != nil && s.certificate != nil && s.certificate.Covers(host)
}
func (s *TLSState) certPath() string {
	return filepath.Join(s.root, s.primary, "current", "fullchain.pem")
}
func (s *TLSState) keyPath() string {
	return filepath.Join(s.root, s.primary, "current", "privkey.pem")
}

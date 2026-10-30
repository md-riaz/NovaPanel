package nginx

import (
	"path/filepath"
	"time"

	certstore "novapanel/internal/tls"
)

// TLSState is an immutable snapshot of a validated installed pair. Only LoadTLS
// can construct a usable value; staging is trusted agent-owned provenance.
type TLSState struct {
	root, primary string
	staging       bool
	certificate   *certstore.Certificate
}

// LoadTLS validates the installed pair and primary SAN. Additional aliases are
// partitioned by actual SAN coverage, not by persisted cert_state labels.
func LoadTLS(root, primary string, staging bool, now time.Time) (*TLSState, error) {
	c, err := certstore.Load(root, primary, []string{primary}, now)
	if err != nil {
		return nil, err
	}
	return &TLSState{root: root, primary: primary, staging: staging, certificate: c}, nil
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

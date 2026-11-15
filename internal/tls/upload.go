package tls

import (
	"bytes"
	"encoding/json"
	"io"
	"novapanel/internal/secret"
	"time"
)

type upload struct {
	Version int    `json:"version"`
	Cert    []byte `json:"cert"`
	Key     []byte `json:"key"`
}

// SealUpload must run before constructing durable job args, not inside Exec.
func SealUpload(s *secret.Sealer, certPEM, keyPEM []byte, domains []string, now time.Time) (secret.Sealed, error) {
	if s == nil {
		return secret.Sealed{}, invalid()
	}
	c, err := Validate(certPEM, keyPEM, domains, now)
	if err != nil {
		return secret.Sealed{}, err
	}
	defer clear(c.keyPEM)
	plain, err := json.Marshal(upload{1, c.certPEM, c.keyPEM})
	if err != nil {
		return secret.Sealed{}, invalid()
	}
	defer clear(plain)
	return s.Seal(plain)
}

// OpenUpload revalidates the pair against execution-time state. The caller owns
// terminal ciphertext destruction and interrupted-job retention in its transaction.
func OpenUpload(s *secret.Sealer, sealed secret.Sealed, domains []string, now time.Time) (*Certificate, error) {
	if s == nil || len(sealed.Ciphertext) > 2*(MaxCertificateBytes+MaxKeyBytes) {
		return nil, invalid()
	}
	plain, err := s.Open(sealed)
	if err != nil {
		return nil, invalid()
	}
	defer clear(plain)
	var in upload
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&in); err != nil {
		return nil, invalid()
	}
	defer clear(in.Key)
	if in.Version != 1 || decoder.Decode(new(any)) != io.EOF {
		return nil, invalid()
	}
	return Validate(in.Cert, in.Key, domains, now)
}

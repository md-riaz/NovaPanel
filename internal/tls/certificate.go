// Package tls validates and installs agent-owned certificate pairs.
package tls

import (
	"bytes"
	stdtls "crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"time"
)

const MaxCertificateBytes = 256 << 10
const MaxKeyBytes = 32 << 10
const MaxDomains = 100

// Certificate deliberately does not export key material or parsed mutable X509 data.
type Certificate struct {
	NotAfter        time.Time `json:"not_after"`
	FQDNs           []string  `json:"fqdns"`
	leaf            *x509.Certificate
	certPEM, keyPEM []byte
}

func (c Certificate) GoString() string { return c.String() }
func (c Certificate) String() string   { return "TLS certificate (key redacted)" }
func (c *Certificate) Covers(domain string) bool {
	return c != nil && c.leaf != nil && validDomain(domain) && c.leaf.VerifyHostname(domain) == nil
}
func invalid() error {
	return rpcerr.Errf("invalid_argument", "Invalid TLS certificate or private key")
}

// pem.Decode searches past malformed blocks; decode only the first block.
func decodePEMBlock(data []byte) (*pem.Block, []byte) {
	data = bytes.TrimLeft(data, " \t\r\n")
	end := bytes.Index(data, []byte("-----END "))
	if end < 0 {
		return nil, nil
	}
	lineEnd := bytes.IndexByte(data[end:], '\n')
	cut := len(data)
	if lineEnd >= 0 {
		cut = end + lineEnd + 1
	}
	if bytes.Count(data[:cut], []byte("-----BEGIN ")) != 1 {
		return nil, nil
	}
	block, tail := pem.Decode(data[:cut])
	if block == nil || len(bytes.TrimSpace(tail)) != 0 {
		return nil, nil
	}
	return block, data[cut:]
}

func validDomain(s string) bool { return (&validate.V{}).Hostname(s) == nil }

// Validate requires SAN coverage for every persisted domain. It does not impose a
// public CA trust policy on manually supplied certificates.
func Validate(certPEM, keyPEM []byte, domains []string, now time.Time) (*Certificate, error) {
	if len(certPEM) == 0 || len(certPEM) > MaxCertificateBytes || len(keyPEM) == 0 || len(keyPEM) > MaxKeyBytes || len(domains) == 0 || len(domains) > MaxDomains {
		return nil, invalid()
	}
	rest := certPEM
	var chain []*x509.Certificate
	for len(bytes.TrimSpace(rest)) > 0 {
		if !bytes.HasPrefix(bytes.TrimSpace(rest), []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, invalid()
		}
		block, tail := decodePEMBlock(rest)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(chain) >= 16 {
			return nil, invalid()
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, invalid()
		}
		if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
			return nil, invalid()
		}
		chain = append(chain, cert)
		rest = tail
	}
	if len(chain) == 0 {
		return nil, invalid()
	}
	block, tail := decodePEMBlock(keyPEM)
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(tail)) != 0 || !bytes.HasPrefix(bytes.TrimSpace(keyPEM), []byte("-----BEGIN "+block.Type+"-----")) {
		return nil, invalid()
	}
	switch block.Type {
	case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
	default:
		return nil, invalid()
	}
	if _, err := stdtls.X509KeyPair(certPEM, keyPEM); err != nil {
		return nil, invalid()
	}
	leaf := chain[0]
	if leaf.IsCA {
		return nil, invalid()
	}
	if len(leaf.ExtKeyUsage) > 0 || len(leaf.UnknownExtKeyUsage) > 0 {
		server := false
		for _, u := range leaf.ExtKeyUsage {
			if u == x509.ExtKeyUsageServerAuth || u == x509.ExtKeyUsageAny {
				server = true
			}
		}
		if !server {
			return nil, invalid()
		}
	}
	for i := 1; i < len(chain); i++ {
		if chain[i-1].CheckSignatureFrom(chain[i]) != nil {
			return nil, invalid()
		}
	}
	c := &Certificate{NotAfter: leaf.NotAfter, FQDNs: append([]string(nil), domains...), leaf: leaf, certPEM: append([]byte(nil), certPEM...), keyPEM: append([]byte(nil), keyPEM...)}
	for _, domain := range domains {
		if !c.Covers(domain) {
			return nil, invalid()
		}
	}
	return c, nil
}

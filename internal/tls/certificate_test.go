package tls

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"novapanel/internal/secret"
	"strings"
	"testing"
	"time"
)

func TestSealedUpload(t *testing.T) {
	cert, key := fixture(t, nil)
	s, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealUpload(s, cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(sealed)
	if bytes.Contains(b, cert) || bytes.Contains(b, key) || strings.Contains(string(b), "PRIVATE KEY") {
		t.Fatal("plaintext persisted")
	}
	opened, err := OpenUpload(s, sealed, []string{"example.test"}, testNow)
	if err != nil || !opened.Covers("example.test") {
		t.Fatal("open failed", err)
	}
	if _, err = OpenUpload(s, sealed, []string{"other.test"}, testNow); err == nil {
		t.Fatal("domain change accepted")
	}
	if _, err = OpenUpload(s, sealed, []string{"example.test"}, testNow.Add(48*time.Hour)); err == nil {
		t.Fatal("expired input accepted")
	}
	sealed.Ciphertext[0] ^= 1
	if _, err = OpenUpload(s, sealed, []string{"example.test"}, testNow); err == nil {
		t.Fatal("tamper accepted")
	}
	if strings.Contains(fmt.Sprintf("%#v", opened), "keyPEM") {
		t.Fatal("debug format exposes private fields")
	}
	if strings.Contains(fmt.Sprintf("%+v", opened), "PRIVATE KEY") {
		t.Fatal("formatted secret")
	}
}

var testNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

// Fixtures are generated in memory with fresh test-only keys, never production trust keys.
func fixture(t *testing.T, change func(*x509.Certificate)) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.test"}, DNSNames: []string{"example.test", "www.example.test"}, NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if change != nil {
		change(tmpl)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})
}

func TestValidateCertificate(t *testing.T) {
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test", "www.example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if c.NotAfter != testNow.Add(24*time.Hour) || !c.Covers("www.example.test") || c.Covers("other.test") {
		t.Fatal("incorrect metadata")
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, key) || strings.Contains(string(b), "PRIVATE KEY") {
		t.Fatal("key serialized")
	}
}

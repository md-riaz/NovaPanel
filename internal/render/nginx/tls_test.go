package nginx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	certstore "novapanel/internal/tls"
	"os"
	"strings"
	"testing"
	"time"
)

var tlsNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func installedTLS(t *testing.T, staging bool) *TLSState {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"demo.test", "www.demo.test"}, NotBefore: tlsNow.Add(-time.Hour), NotAfter: tlsNow.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := certstore.Validate(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}), []string{"demo.test"}, tlsNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = certstore.Install(root, "demo.test", c); err != nil {
		t.Fatal(err)
	}
	state, err := LoadTLS(root, "demo.test", staging, tlsNow)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func TestTLSRenderStates(t *testing.T) {
	d := Desired{Name: "demo", Domains: []string{"demo.test", "www.demo.test", "new.test"}, Docroot: "/srv/sites/demo/public", ACMERoot: "/var/www/acme/demo", TLS: installedTLS(t, false)}
	for _, force := range []bool{false, true} {
		d.ForceHTTPS = force
		d.HSTSMaxAge = 0
		if force {
			d.HSTSMaxAge = 300
		}
		b, err := Render(d)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, want := range []string{"listen 443 ssl http2;", "server_name demo.test www.demo.test;", "server_name new.test;", "/current/fullchain.pem", "/current/privkey.pem"} {
			if !strings.Contains(s, want) {
				t.Fatalf("missing %s: %s", want, s)
			}
		}
		if strings.Contains(s, "return 301 https://$host$request_uri;") != force {
			t.Fatal(s)
		}
		if strings.Contains(s, `Strict-Transport-Security "max-age=300" always;`) != force {
			t.Fatal(s)
		}
		if strings.Contains(s, "preload") {
			t.Fatal(s)
		}
	}
	d.ForceHTTPS = false
	d.HSTSMaxAge = 0
	d.ModernHTTP2 = true
	b, err := Render(d)
	if err != nil || !strings.Contains(string(b), "http2 on;") || strings.Contains(string(b), "ssl http2;") {
		t.Fatalf("modern HTTP2: %s %v", b, err)
	}
}

package nginx

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	certstore "novapanel/internal/tls"
)

func manualProbePair(t *testing.T, now time.Time, names []string) (tls.Certificate, *TLSState) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: names, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Minute), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kd, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})
	validated, err := certstore.Validate(certPEM, keyPEM, []string{names[0]}, now)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := certstore.Install(root, names[0], validated); err != nil {
		t.Fatal(err)
	}
	state, err := LoadTLS(root, names[0], false, now)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return pair, state
}

func TestStateProbeApprovedManualIdentity(t *testing.T) {
	now := time.Now()
	pair, state := manualProbePair(t, now, []string{"demo.test", "www.demo.test"})
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	secure := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != r.TLS.ServerName {
			t.Errorf("Host/SNI mismatch: %q/%q", r.Host, r.TLS.ServerName)
		}
		w.WriteHeader(403)
	}))
	secure.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	secure.StartTLS()
	defer secure.Close()
	d := Desired{Domains: []string{"demo.test", "www.demo.test"}, TLS: state}
	if err := StateProbe(plain.URL, secure.URL, nil)(context.Background(), d); err != nil {
		t.Fatalf("approved private CA identity refused: %v", err)
	}
	// Without an approved identity, retain normal system-root verification.
	unapproved := d
	unapproved.TLS = &TLSState{certificate: state.certificate}
	if err := StateProbe(plain.URL, secure.URL, nil)(context.Background(), unapproved); err == nil {
		t.Fatal("private CA trusted without approved identity")
	}
	if err := StateProbe(plain.URL, secure.URL, &tls.Config{VerifyConnection: func(tls.ConnectionState) error { return context.Canceled }})(context.Background(), d); err == nil {
		t.Fatal("custom verification callback bypassed")
	}
	other, _ := manualProbePair(t, now, []string{"demo.test", "www.demo.test"})
	wrong := httptest.NewUnstartedServer(http.NotFoundHandler())
	wrong.TLS = &tls.Config{Certificates: []tls.Certificate{other}}
	wrong.StartTLS()
	defer wrong.Close()
	if err := StateProbe(plain.URL, wrong.URL, nil)(context.Background(), d); err == nil {
		t.Fatal("accepted different served leaf")
	}
	for _, at := range []time.Time{now.Add(-2 * time.Minute), now.Add(2 * time.Minute)} {
		if err := StateProbe(plain.URL, secure.URL, &tls.Config{Time: func() time.Time { return at }})(context.Background(), d); err == nil {
			t.Fatal("accepted leaf outside validity period")
		}
	}
}

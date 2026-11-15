package verbs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCA only serves loopback TLS. It signs the actual lego CSR and checks that
// HTTP-01 challenge bytes are present before approving authorization.
type fakeCA struct {
	mu                                          sync.Mutex
	server                                      *httptest.Server
	root                                        string
	domains                                     []string
	cert                                        []byte
	replacements                                []string
	orders, registrations, challenges, requests int
	fail                                        bool
	lifetime                                    time.Duration
}

func newFakeCA(t *testing.T, root string) *fakeCA {
	t.Helper()
	f := &fakeCA{root: root, lifetime: 12 * time.Hour}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test-only CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, SubjectKeyId: []byte{1, 2, 3, 4}}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(caDER)
	if e != nil {
		t.Fatal(e)
	}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		base := f.server.URL
		w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("nonce-%032d", f.requests))))
		w.Header().Set("Content-Type", "application/json")
		payload := []byte("{}")
		if r.Method == "POST" {
			var jws struct {
				Payload string `json:"payload"`
			}
			body, e := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			if json.Unmarshal(body, &jws) != nil {
				t.Error("invalid JWS envelope")
				w.WriteHeader(400)
				return
			}
			payload, e = base64.RawURLEncoding.DecodeString(jws.Payload)
			if e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
		}
		write := func(v any) {
			if e := json.NewEncoder(w).Encode(v); e != nil {
				t.Error(e)
			}
		}
		switch r.URL.Path {
		case "/directory":
			write(map[string]any{"newNonce": base + "/nonce", "newAccount": base + "/account", "newOrder": base + "/order", "revokeCert": base + "/revoke", "keyChange": base + "/key-change", "renewalInfo": base + "/renewal-info"})
		case "/nonce":
			w.WriteHeader(200)
		case "/account":
			f.registrations++
			w.Header().Set("Location", base+"/account/1")
			w.WriteHeader(201)
			write(map[string]any{"status": "valid"})
		case "/order":
			f.orders++
			var order struct {
				Identifiers []struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"identifiers"`
				Replaces string `json:"replaces"`
			}
			if json.Unmarshal(payload, &order) != nil {
				t.Error("invalid order")
				w.WriteHeader(400)
				return
			}
			if f.fail {
				w.WriteHeader(400)
				write(map[string]any{"type": "urn:ietf:params:acme:error:rejectedIdentifier", "detail": "PRIVATE CA FAILURE"})
				return
			}
			f.domains = nil
			for _, id := range order.Identifiers {
				f.domains = append(f.domains, id.Value)
			}
			f.replacements = append(f.replacements, order.Replaces)
			auths := []string{}
			for i := range f.domains {
				auths = append(auths, fmt.Sprintf("%s/auth/%d", base, i))
			}
			w.Header().Set("Location", base+"/order/1")
			w.WriteHeader(201)
			write(map[string]any{"status": "pending", "identifiers": order.Identifiers, "authorizations": auths, "finalize": base + "/finalize"})
		case "/finalize":
			var data struct {
				CSR string `json:"csr"`
			}
			if json.Unmarshal(payload, &data) != nil {
				t.Error("bad CSR JSON")
				w.WriteHeader(400)
				return
			}
			der, e := base64.RawURLEncoding.DecodeString(data.CSR)
			if e != nil {
				t.Error(e)
				w.WriteHeader(400)
				return
			}
			csr, e := x509.ParseCertificateRequest(der)
			if e != nil || csr.CheckSignature() != nil {
				t.Error("bad CSR")
				w.WriteHeader(400)
				return
			}
			if !reflect.DeepEqual(csr.DNSNames, f.domains) {
				t.Errorf("CSR SANs %v != order %v", csr.DNSNames, f.domains)
			}
			leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(f.orders + 10)), Subject: csr.Subject, DNSNames: csr.DNSNames, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(f.lifetime), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, AuthorityKeyId: ca.SubjectKeyId}
			issued, e := x509.CreateCertificate(rand.Reader, leaf, ca, csr.PublicKey, key)
			if e != nil {
				t.Error(e)
				w.WriteHeader(500)
				return
			}
			f.cert = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issued}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
			write(map[string]any{"status": "valid", "certificate": base + "/cert"})
		case "/cert":
			w.Header().Set("Content-Type", "application/pem-certificate-chain")
			w.Write(f.cert)
		default:
			var i int
			if strings.HasPrefix(r.URL.Path, "/auth/") {
				fmt.Sscanf(r.URL.Path, "/auth/%d", &i)
				write(map[string]any{"status": "pending", "identifier": map[string]string{"type": "dns", "value": f.domains[i]}, "challenges": []any{map[string]any{"type": "http-01", "url": fmt.Sprintf("%s/challenge/%d", base, i), "token": fmt.Sprintf("token%020d", i), "status": "pending"}}})
			} else if strings.HasPrefix(r.URL.Path, "/challenge/") {
				fmt.Sscanf(r.URL.Path, "/challenge/%d", &i)
				token := fmt.Sprintf("token%020d", i)
				b, e := os.ReadFile(filepath.Join(root, ".well-known/acme-challenge", token))
				if e != nil || !strings.HasPrefix(string(b), token+".") || len(b) != len(token)+44 {
					t.Errorf("HTTP01 was not published: %q, %v", b, e)
					w.WriteHeader(400)
					return
				}
				f.challenges++
				write(map[string]any{"type": "http-01", "url": base + r.URL.Path, "status": "valid", "token": token})
			} else {
				t.Errorf("unexpected CA request %s", r.URL.Path)
				w.WriteHeader(404)
			}
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

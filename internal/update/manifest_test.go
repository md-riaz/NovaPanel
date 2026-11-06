package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"novapanel/internal/rpcerr"
	"strings"
	"testing"
)

func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *rpcerr.CodedError
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}
func signed(t *testing.T, body string) (ed25519.PublicKey, []byte) {
	t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return pub, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(body))))
}

const goodManifest = `{"version":"1.2.3","security_fix":true,"artifacts":[{"os":"linux","arch":"amd64","filename":"panel.tar","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":3,"url":"https://releases.example/panel.tar"}]}`

func TestManifestTrustAndSelection(t *testing.T) {
	pub, sig := signed(t, goodManifest)
	m, a, err := verifyManifest(pub, []byte(goodManifest), sig, "dev", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.2.3" || !m.SecurityFix || a.Size != 3 {
		t.Fatal(m, a)
	}
	_, _, err = Verify([]byte(goodManifest), sig, "dev")
	code(t, err, "unsupported")
	for _, installed := range []string{"1.2.3", "1.2.3+build", "2.0.0", "999999999999999999999999.0.0"} {
		_, _, err = verifyManifest(pub, []byte(goodManifest), sig, installed, "linux", "amd64")
		code(t, err, "precondition_failed")
	}
	for _, installed := range []string{"0.0.0", "1.2.3-rc.1", "junk", "01.2.3"} {
		_, _, err = verifyManifest(pub, []byte(goodManifest), sig, installed, "linux", "amd64")
		if err != nil {
			t.Fatal(installed, err)
		}
	}
	_, _, err = verifyManifest(pub, []byte(goodManifest), sig, "dev", "other", "amd64")
	code(t, err, "unsupported")
}
func TestManifestRejectsAuthenticatedInvalidInput(t *testing.T) {
	for _, body := range []string{
		"not JSON", goodManifest + " {}", strings.Replace(goodManifest, `"version":"1.2.3"`, `"version":"1.2.3","version":"9.0.0"`, 1),
		strings.Replace(goodManifest, "1.2.3", "v1.2.3", 1), strings.Replace(goodManifest, "1.2.3", "01.2.3", 1), strings.Replace(goodManifest, "1.2.3", "1.2.3-rc.1", 1), strings.Replace(goodManifest, "1.2.3", "1.2", 1), strings.Replace(goodManifest, "1.2.3", "1.2.3+", 1),
		strings.Replace(goodManifest, "https://", "http://", 1), strings.Replace(goodManifest, "https://", "https://user:password@", 1), strings.Replace(goodManifest, "panel.tar\"", "../panel.tar\"", 1), strings.Replace(goodManifest, `"size":3`, `"size":0`, 1), strings.Replace(goodManifest, `"size":3`, `"size":536870913`, 1), strings.Replace(goodManifest, `"security_fix":true`, `"unknown":true`, 1), strings.Replace(goodManifest, strings.Repeat("a", 64), "oops", 1),
	} {
		pub, sig := signed(t, body)
		_, _, err := verifyManifest(pub, []byte(body), sig, "dev", "linux", "amd64")
		code(t, err, "invalid_argument")
	}
	pub, sig := signed(t, "not JSON")
	_, _, err := verifyManifest(pub, []byte("different"), sig, "dev", "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatal(err)
	}
	_, _, err = verifyManifest(pub, make([]byte, maxManifestBytes+1), sig, "dev", "linux", "amd64")
	code(t, err, "invalid_argument")
}

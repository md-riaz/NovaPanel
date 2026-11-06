package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"novapanel/internal/rpcerr"
)

func TestVerifySignatureExactBytes(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately invalid JSON: authentication must not parse the manifest.
	manifest := []byte("not JSON\n")
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifest))
	if err := verifySignature(public, manifest, []byte(" \t\n"+signature+"\r\n")); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySignatureRejectsMalformedOrUnauthenticated(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"version":"1.2.3"}`)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(private, manifest))
	for _, tc := range []struct {
		name string
		key  ed25519.PublicKey
		body []byte
		sig  string
	}{
		{"changed bytes", public, append(append([]byte{}, manifest...), '\n'), sig},
		{"wrong key", other, manifest, sig},
		{"missing key", nil, manifest, sig},
		{"short key", public[:31], manifest, sig},
		{"oversized key", append(append(ed25519.PublicKey{}, public...), 0), manifest, sig},
		{"raw base64", public, manifest, strings.TrimRight(sig, "=")},
		{"embedded LF", public, manifest, sig[:10] + "\n" + sig[10:]},
		{"embedded CRLF", public, manifest, sig[:10] + "\r\n" + sig[10:]},
		{"embedded space", public, manifest, sig[:10] + " " + sig[10:]},
		{"short signature", public, manifest, base64.StdEncoding.EncodeToString(make([]byte, 63))},
		{"long signature", public, manifest, base64.StdEncoding.EncodeToString(make([]byte, 65))},
		{"empty signature", public, manifest, ""},
		{"invalid alphabet", public, manifest, "!" + sig[1:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifySignature(tc.key, tc.body, []byte(tc.sig))
			var coded *rpcerr.CodedError
			if !errors.As(err, &coded) || coded.Code != "invalid_argument" {
				t.Fatalf("got %v, want invalid_argument", err)
			}
		})
	}
}

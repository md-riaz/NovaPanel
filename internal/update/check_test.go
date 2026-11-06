package update

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCheckTrustGateAndBoundedFetch(t *testing.T) {
	calls := 0
	body := goodManifest
	pub, sig := signed(t, body)
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		text := body
		if strings.HasSuffix(r.URL.Path, ".sig") {
			text = string(sig)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(text)), Header: make(http.Header)}, nil
	})}
	_, _, err := check(context.Background(), nil, client, "https://releases.example/manifest.json", "dev")
	code(t, err, "unsupported")
	if calls != 0 {
		t.Fatal("network before trust")
	}
	_, _, err = check(context.Background(), pub, client, "https://releases.example/manifest.json", "dev")
	if err != nil || calls != 2 {
		t.Fatal(calls, err)
	}
	body = strings.Repeat("x", maxManifestBytes+1)
	_, _, err = check(context.Background(), pub, client, "https://releases.example/manifest.json", "dev")
	code(t, err, "invalid_argument")
	for _, raw := range []string{"http://releases.example/m", "https://user@host/m", "https://host/m#frag"} {
		_, _, err = check(context.Background(), pub, client, raw, "dev")
		code(t, err, "invalid_argument")
	}
}
func TestReleaseKeyEncoding(t *testing.T) {
	old := releasePublicKeyBase64
	defer func() { releasePublicKeyBase64 = old }()
	for _, v := range []string{"", "broken", base64.StdEncoding.EncodeToString(make([]byte, 31)), base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize)) + "\n"} {
		releasePublicKeyBase64 = v
		_, err := releaseKey()
		code(t, err, "unsupported")
	}
	pub, _ := signed(t, goodManifest)
	releasePublicKeyBase64 = base64.StdEncoding.EncodeToString(pub)
	if _, err := releaseKey(); err != nil {
		t.Fatal(err)
	}
}

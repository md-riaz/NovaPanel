package tls

import (
	"bytes"
	"encoding/json"
	"fmt"
	"novapanel/internal/secret"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadVersionRefused(t *testing.T) {
	cert, key := fixture(t, nil)
	s, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, 2} {
		plain, err := json.Marshal(upload{version, cert, key})
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := s.Seal(plain)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := OpenUpload(s, sealed, []string{"example.test"}, testNow); err == nil {
			t.Fatalf("version %d accepted", version)
		}
	}
}

func TestInstallRefusesRootSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "real")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(link, "example.test", c); err == nil {
		t.Fatal("symlink root accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("symlink root changed", err)
	}
}

func TestMalformedLeadingPEMBlock(t *testing.T) {
	cert, key := fixture(t, nil)
	malformed := []byte("-----BEGIN CERTIFICATE-----\nnot base64!\n-----END CERTIFICATE-----\n")
	malformedKey := []byte("-----BEGIN PRIVATE KEY-----\nnot base64!\n-----END PRIVATE KEY-----\n")
	if _, err := Validate(cert, append(malformedKey, key...), []string{"example.test"}, testNow); err == nil {
		t.Fatal("malformed leading key block skipped")
	}
	if _, err := Validate(append(malformed, cert...), key, []string{"example.test"}, testNow); err == nil {
		t.Fatal("malformed leading certificate block skipped")
	}
}

func TestCertificateValueFormattingRedactsKey(t *testing.T) {
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if got := fmt.Sprintf(format, *c); strings.Contains(got, "keyPEM") || strings.Contains(got, "leaf:") {
			t.Fatalf("value formatting exposes private fields with %s", format)
		}
	}
}

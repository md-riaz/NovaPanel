package tls

import (
	"bytes"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenewalSourceRetainsPairValidation(t *testing.T) {
	for _, kind := range []string{"expired", "future", "wrong-primary", "mismatched-key", "unsafe-mode", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			cert, key := fixture(t, nil)
			c, err := Validate(cert, key, []string{"example.test"}, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Install(root, "example.test", c); err != nil {
				t.Fatal(err)
			}
			now := testNow.Add(48 * time.Hour)
			switch kind {
			case "future":
				now = testNow.Add(-48 * time.Hour)
			case "wrong-primary":
				cert, _ = fixture(t, func(c *x509.Certificate) { c.DNSNames = []string{"other.test"} })
			case "mismatched-key":
				_, key = fixture(t, nil)
			}
			current := filepath.Join(root, "example.test/current")
			for name, data := range map[string][]byte{"fullchain.pem": cert, "privkey.pem": key} {
				if err = os.WriteFile(filepath.Join(current, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "unsafe-mode" {
				if err = os.Chmod(filepath.Join(current, "privkey.pem"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink" {
				if err = os.Rename(filepath.Join(current, "fullchain.pem"), filepath.Join(current, "retained.pem")); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink("retained.pem", filepath.Join(current, "fullchain.pem")); err != nil {
					t.Fatal(err)
				}
			}
			got, err := LoadRenewalSource(root, "example.test", now)
			if kind == "expired" {
				if err != nil || !bytes.Equal(got, cert) {
					t.Fatalf("expired renewal source: %v", err)
				}
				if _, err = Load(root, "example.test", []string{"example.test"}, now); err == nil {
					t.Fatal("expired serving pair accepted")
				}
			} else if err == nil {
				t.Fatal("unsafe renewal source accepted")
			}
		})
	}
}

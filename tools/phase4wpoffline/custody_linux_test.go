//go:build wordpress_offline_lab && linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/wordpress/offline"
)

func TestLabArtifactPins(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned fixture required")
	}
	d := t.TempDir()
	for _, name := range []string{"bootstrap.php", "transport.php"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(d, name), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, url := range map[string]string{
		"cli.phar":       "https://github.com/wp-cli/wp-cli/releases/download/v2.12.0/wp-cli-2.12.0.phar",
		"checksums.json": "https://api.wordpress.org/core/checksums/1.0/?version=7.1.4&locale=en_US",
	} {
		b, err := offline.Resolve(url)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(d, name), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := labArtifacts(d); err != nil {
		t.Fatal(err)
	}
}

func TestLabArtifactCustody(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned fixture required")
	}
	for _, name := range []string{"bootstrap.php", "transport.php", "cli.phar", "checksums.json"} {
		t.Run(name, func(t *testing.T) {
			b := []byte("fixture content for " + name)
			digest := sha256.Sum256(b)
			want := hex.EncodeToString(digest[:])
			d := t.TempDir()
			p := filepath.Join(d, name)
			if err := os.WriteFile(p, b, 0644); err != nil {
				t.Fatal(err)
			}
			check := func() error { return checkLabFiles(d, map[string]string{name: want}) }
			if err := check(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, append(b, 'x'), 0644); err != nil {
				t.Fatal(err)
			}
			if check() == nil {
				t.Fatal("accepted modified bytes")
			}
			if err := os.WriteFile(p, b, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(p, 23041, 23041); err != nil {
				t.Fatal(err)
			}
			if check() == nil {
				t.Fatal("accepted foreign owner")
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc/passwd", p); err != nil {
				t.Fatal(err)
			}
			if check() == nil {
				t.Fatal("accepted symlink")
			}
		})
	}
}

func TestLabConfigCustody(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-owned fixture required")
	}
	b, err := os.ReadFile("php.ini")
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	p := filepath.Join(d, "php.ini")
	if err = os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
	if err = labConfig(d); err != nil {
		t.Fatalf("valid ini: %v", err)
	}
	if err = os.WriteFile(p, append(b, 'x'), 0644); err != nil {
		t.Fatal(err)
	}
	if labConfig(d) == nil {
		t.Fatal("accepted tampered ini")
	}
	if err = os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/etc/passwd", p); err != nil {
		t.Fatal(err)
	}
	if labConfig(d) == nil {
		t.Fatal("accepted symlink")
	}
}

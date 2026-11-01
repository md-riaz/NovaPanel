package tls

import (
	"crypto/x509"
	"encoding/asn1"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLoadRefusesUnsafeInstalledEntries(t *testing.T) {
	for _, kind := range []string{"fifo", "oversized", "version escape", "version symlink", "site symlink", "root mode", "version mode"} {
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
			version, err := Install(root, "example.test", c)
			if err != nil {
				t.Fatal(err)
			}
			site := filepath.Join(root, "example.test")
			dir := filepath.Join(site, version)
			file := filepath.Join(dir, "privkey.pem")
			switch kind {
			case "fifo":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
				if err := unix.Mkfifo(file, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.Truncate(file, MaxKeyBytes+1); err != nil {
					t.Fatal(err)
				}
			case "version escape":
				if err := os.Remove(filepath.Join(site, "current")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../"+version, filepath.Join(site, "current")); err != nil {
					t.Fatal(err)
				}
			case "version symlink":
				if err := os.Rename(dir, dir+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(version+"-saved", dir); err != nil {
					t.Fatal(err)
				}
			case "site symlink":
				if err := os.Rename(site, site+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("example.test-saved", site); err != nil {
					t.Fatal(err)
				}
			case "root mode":
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			case "version mode":
				if err := os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { _, err := Load(root, "example.test", []string{"example.test"}, testNow); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe installed entry accepted")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("installed entry blocked Load")
			}
		})
	}
}

func TestLoadKeepsPairConsistentDuringCurrentSwap(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	var versions []string
	for i := 0; i < 2; i++ {
		cert, key := fixture(t, nil)
		c, err := Validate(cert, key, []string{"example.test"}, testNow)
		if err != nil {
			t.Fatal(err)
		}
		version, err := Install(root, "example.test", c)
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	site := filepath.Join(root, "example.test")
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			temp := filepath.Join(site, ".swap")
			if err := os.Symlink(versions[i%2], temp); err != nil {
				done <- err
				return
			}
			if err := os.Rename(temp, filepath.Join(site, "current")); err != nil {
				done <- err
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 100; i++ {
		if _, err := Load(root, "example.test", []string{"example.test"}, testNow); err != nil {
			t.Fatalf("valid atomic pointer swap broke installed pair: %v", err)
		}
	}
}

func TestValidateRefusesUnknownOnlyExtendedKeyUsage(t *testing.T) {
	cert, key := fixture(t, func(c *x509.Certificate) {
		c.ExtKeyUsage = nil
		c.UnknownExtKeyUsage = []asn1.ObjectIdentifier{{1, 3, 6, 1, 4, 1, 55555, 1}}
	})
	if _, err := Validate(cert, key, []string{"example.test"}, testNow); err == nil {
		t.Fatal("certificate restricted to an unknown purpose accepted for TLS server use")
	}
}

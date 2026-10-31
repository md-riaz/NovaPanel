package tls

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadInstalledCertificate(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, "example.test", c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root, "example.test", []string{"example.test", "www.example.test"}, testNow)
	if err != nil || !loaded.Covers("www.example.test") {
		t.Fatal("load", err)
	}
	if _, err := Load(root, "example.test", []string{"other.test"}, testNow); err == nil {
		t.Fatal("uncovered domain accepted")
	}
	if _, err := Load(root, "example.test", []string{"example.test"}, testNow.Add(48*time.Hour)); err == nil {
		t.Fatal("expired pair accepted")
	}
	current := filepath.Join(root, "example.test", "current")
	if err := os.Chmod(filepath.Join(current, "privkey.pem"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "example.test", []string{"example.test"}, testNow); err == nil {
		t.Fatal("unsafe key mode accepted")
	}
}

func TestLoadRefusesSymlinkedCertificateFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	cert, key := fixture(t, nil)
	c, err := Validate(cert, key, []string{"example.test"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, "example.test", c); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "example.test", "current", "privkey.pem")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("fullchain.pem", file); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root, "example.test", []string{"example.test"}, testNow); err == nil {
		t.Fatal("symlinked key accepted")
	}
}

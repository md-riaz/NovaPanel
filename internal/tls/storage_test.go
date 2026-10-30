package tls

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPair(t *testing.T) {
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
	link, err := os.Readlink(filepath.Join(root, "example.test", "current"))
	if err != nil || link != version {
		t.Fatal("current", link, err)
	}
	for name, want := range map[string][]byte{"fullchain.pem": cert, "privkey.pem": key} {
		b, err := os.ReadFile(filepath.Join(root, "example.test", "current", name))
		if err != nil || string(b) != string(want) {
			t.Fatal("pair", err)
		}
		st, err := os.Stat(filepath.Join(root, "example.test", version, name))
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal("mode", err)
		}
	}
	next, err := Install(root, "example.test", c)
	if err != nil || next == version {
		t.Fatal("version", err)
	}
	if _, err = Install(root, "../escape", c); err == nil {
		t.Fatal("traversal accepted")
	}
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(root, "www.example.test")); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(root, "www.example.test", c); err == nil {
		t.Fatal("symlink parent accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside root", err)
	}
	if err = os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(root, "example.test", c); err == nil {
		t.Fatal("unsafe root mode accepted")
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "example.test", "current")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("../outside", filepath.Join(root, "example.test", "current")); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(root, "example.test", c); err == nil {
		t.Fatal("unexpected current target accepted")
	}
	link, err = os.Readlink(filepath.Join(root, "example.test", "current"))
	if err != nil || link != "../outside" {
		t.Fatal("unsafe current changed", err)
	}
}

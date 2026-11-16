//go:build wordpress_offline_lab && linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

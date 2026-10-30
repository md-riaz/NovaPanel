package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.pid")
	first, e := lockInstance(p)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := lockInstance(p); e == nil {
		other.Close()
		t.Fatal("second instance accepted")
	}
	if e := first.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := lockInstance(p)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	b, e := os.ReadFile(p)
	if e != nil || len(b) == 0 {
		t.Fatalf("missing live pid: %q %v", b, e)
	}
}
func TestInstanceLockRejectsSymlink(t *testing.T) {
	d := t.TempDir()
	target := filepath.Join(d, "target")
	if e := os.WriteFile(target, []byte("untouched"), 0600); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(d, "pid")
	if e := os.Symlink(target, p); e != nil {
		t.Fatal(e)
	}
	if f, e := lockInstance(p); e == nil {
		f.Close()
		t.Fatal("accepted symlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "untouched" {
		t.Fatal("modified target")
	}
}

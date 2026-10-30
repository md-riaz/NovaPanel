package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseFileSafetyAndInstanceLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if other, e := Open(p); e == nil {
		other.Close()
		t.Fatal("second agent accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if e = os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if other, e := Open(link); e == nil {
		other.Close()
		t.Fatal("symlink accepted")
	}
	s.Close()
	if e = os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if other, e := Open(p); e == nil {
		other.Close()
		t.Fatal("public database accepted")
	}
}

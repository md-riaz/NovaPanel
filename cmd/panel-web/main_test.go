package main

import "testing"

func TestRefusesRoot(t *testing.T) {
	old := geteuid
	defer func() { geteuid = old }()
	geteuid = func() int { return 0 }
	if err := run(); err == nil {
		t.Fatal("web started as root")
	}
	geteuid = func() int { return 1001 }
	if err := run(); err != nil {
		t.Fatal(err)
	}
}

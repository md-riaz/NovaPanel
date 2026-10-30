package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentRootAndSocketSafety(t *testing.T) {
	old := geteuid
	defer func() { geteuid = old }()
	geteuid = func() int { return 1001 }
	if err := run(nil); err == nil {
		t.Fatal("agent started unprivileged")
	}
	p := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(p, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if l, err := listenSocket(p, 0); err == nil {
		l.Close()
		t.Fatal("overwrote existing path")
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "keep" {
		t.Fatal("existing path changed")
	}
}

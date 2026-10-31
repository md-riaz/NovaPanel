package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPoolPublicationRefusesUnmanagedPool(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "demo.conf"), []byte("unmanaged"), 0644)
	r := &applyRunner{}
	a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return nil }}
	if err := a.Apply(context.Background(), Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}); err == nil {
		t.Fatal("adopted unmanaged pool")
	}
	b, _ := os.ReadFile(filepath.Join(root, "demo.conf"))
	if string(b) != "unmanaged" || len(r.calls) != 0 {
		t.Fatal("modified unmanaged pool")
	}
}

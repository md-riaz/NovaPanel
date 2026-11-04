package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPoolPublicationRefusesWritableCustody(t *testing.T) {
	for _, leaf := range []string{".", "demo.conf", "demo.conf.lkg"} {
		t.Run(leaf, func(t *testing.T) {
			root := t.TempDir()
			if leaf == "." {
				os.Chmod(root, 0777)
			} else {
				if err := os.WriteFile(filepath.Join(root, leaf), []byte("untrusted"), 0666); err != nil {
					t.Fatal(err)
				}
				os.Chmod(filepath.Join(root, leaf), 0666)
			}
			r := &applyRunner{}
			a := &Applier{Root: root, Runner: r, Probe: func(context.Context, Desired) error { return nil }}
			if err := a.Apply(context.Background(), Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}); err == nil {
				t.Fatal("accepted writable custody")
			}
			if len(r.calls) != 0 {
				t.Fatal(r.calls)
			}
		})
	}
}

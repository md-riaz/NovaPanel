package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The recovery snapshot must survive promotion of a newer LKG in the same transaction.
func TestTransactionSnapshotRetainsPriorLKG(t *testing.T) {
	root := t.TempDir()
	old := []byte("; novapanel managed pool\nold\n")
	for _, name := range []string{"demo.conf", "demo.conf.lkg"} {
		if err := os.WriteFile(filepath.Join(root, name), old, 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := &Applier{Root: root, Runner: &applyRunner{}, Probe: func(context.Context, Desired) error { return nil }}
	d := Desired{Name: "demo", Type: "php", Version: "8.3", UID: 1001, GID: 1001}
	err := a.Transaction(context.Background(), d, func(tx *Transaction) error {
		snap := tx.Snapshot()
		if err := tx.Stage(); err != nil {
			return err
		}
		if err := tx.Promote(); err != nil {
			return err
		}
		return tx.RestoreSnapshot(snap)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo.conf", "demo.conf.lkg"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != string(old) {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
}

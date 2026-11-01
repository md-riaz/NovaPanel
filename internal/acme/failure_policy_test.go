package acme_test

import (
	"context"
	"errors"
	"novapanel/internal/acme"
	"os"
	"testing"
)

func TestAmbiguousCAFailureChargesPolicy(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0755)
	f := newFakeCA(t, root)
	_, cfg, record := setupClient(t, f, func(acme.Order) error { return nil }, func(acme.AccountRecord) error { return nil })
	charged := 0
	cfg.FailedOrder = func(o acme.Order) error { charged++; return nil }
	f.server.Close()
	c, e := acme.New(cfg, record)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.Issue(context.Background(), []string{"example.test"}, root)
	if !errors.Is(e, acme.ErrCA) || charged != 1 {
		t.Fatalf("failure %v charges %d", e, charged)
	}
}

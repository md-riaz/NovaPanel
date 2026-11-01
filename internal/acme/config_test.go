package acme_test

import (
	"testing"

	"novapanel/internal/acme"
)

func TestClientRequiresDurablePolicyAndSaveCallbacks(t *testing.T) {
	root := t.TempDir()
	f := newFakeCA(t, root)
	_, cfg, record := setupClient(t, f, func(acme.Order) error { return nil }, func(acme.AccountRecord) error { return nil })
	missing := cfg
	missing.BeforeOrder = nil
	if _, e := acme.New(missing, record); e == nil {
		t.Fatal("accepted missing policy")
	}
	missing = cfg
	missing.SaveAccount = nil
	if _, e := acme.New(missing, record); e == nil {
		t.Fatal("accepted missing durable account save")
	}
	if _, e := acme.NewAccount(cfg.Sealer, acme.Production, cfg.Directory, ""); e == nil {
		t.Fatal("accepted production without email")
	}
	if _, e := acme.NewAccount(cfg.Sealer, acme.Staging, "http://127.0.0.1/directory", ""); e == nil {
		t.Fatal("accepted plaintext CA transport")
	}
	record.Name = "acme:account:prod"
	cfg.Environment = acme.Production
	if _, e := acme.New(cfg, record); e == nil {
		t.Fatal("renaming ciphertext bypassed environment binding")
	}
}

package acme

import (
	"context"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"testing"
)

func TestPersistedClientKeepsKeyAndSeparatesAccounts(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sealer, _ := secret.New(make([]byte, 32))
	cfg := Config{Environment: Staging, Directory: "https://ca.invalid/directory", Sealer: sealer}
	now := func() int64 { return 2000000000000 }
	if _, e = NewPersisted(ctx, s, cfg, now); e != nil {
		t.Fatal(e)
	}
	before, e := s.ACMEAccount(ctx, "acme:account:staging")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewPersisted(ctx, s, cfg, now); e != nil {
		t.Fatal(e)
	}
	after, _ := s.ACMEAccount(ctx, "acme:account:staging")
	if string(before.Ciphertext) != string(after.Ciphertext) {
		t.Fatal("key replaced")
	}
	cfg.Environment = Production
	if _, e = NewPersisted(ctx, s, cfg, now); e == nil {
		t.Fatal("empty production email accepted")
	}
	cfg.Email = "ops@example.com"
	if _, e = NewPersisted(ctx, s, cfg, now); e != nil {
		t.Fatal(e)
	}
	cfg.Environment = Staging
	if _, e = NewPersisted(ctx, s, cfg, now); e == nil {
		t.Fatal("account configuration rebound")
	}
}

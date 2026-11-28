package store

import (
	"context"
	"novapanel/internal/id"
	"novapanel/internal/secret"
	"testing"
)

func TestCreateIntegrationSealedAtomically(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal([]byte("example-token"))
	if err != nil {
		t.Fatal(err)
	}
	integrationID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	v := ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}
	if err = s.CreateIntegration(context.Background(), integrationID, `{"kind":"cloudflare","zones":["example.com"]}`, v); err != nil {
		t.Fatal(err)
	}
	var nonce, ciphertext []byte
	if err = s.db.QueryRowContext(context.Background(), "SELECT nonce,ciphertext FROM secrets WHERE name=?", "integration:"+integrationID+":token").Scan(&nonce, &ciphertext); err != nil {
		t.Fatal(err)
	}
	plain, err := sealer.Open(secret.Sealed{Nonce: nonce, Ciphertext: ciphertext})
	if err != nil || string(plain) != "example-token" {
		t.Fatal("sealed secret failed round trip")
	}
	if err = s.CreateIntegration(context.Background(), integrationID, `{}`, v); err == nil {
		t.Fatal("duplicate accepted")
	}
}

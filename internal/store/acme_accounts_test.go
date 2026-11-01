package store

import (
	"bytes"
	"context"
	"testing"
)

func TestACMEAccountCreateOnlyAndIsolation(t *testing.T) {
	s, e := Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	first := ACMESealed{Nonce: make([]byte, 12), Ciphertext: bytes.Repeat([]byte{1}, 32)}
	second := ACMESealed{Nonce: make([]byte, 12), Ciphertext: bytes.Repeat([]byte{2}, 32)}
	got, e := s.CreateACMEAccount(ctx, "acme:account:staging", first)
	if e != nil {
		t.Fatal(e)
	}
	got, e = s.CreateACMEAccount(ctx, "acme:account:staging", second)
	if e != nil || !bytes.Equal(got.Ciphertext, first.Ciphertext) {
		t.Fatal("replaced existing key", e)
	}
	if _, e = s.CreateACMEAccount(ctx, "acme:account:prod", second); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveACMEAccount(ctx, "acme:account:staging", second); e != nil {
		t.Fatal(e)
	}
	got, e = s.ACMEAccount(ctx, "acme:account:prod")
	if e != nil || !bytes.Equal(got.Ciphertext, second.Ciphertext) {
		t.Fatal(e)
	}
	if _, e = s.ACMEAccount(ctx, "arbitrary"); e == nil {
		t.Fatal("accepted arbitrary secret name")
	}
}

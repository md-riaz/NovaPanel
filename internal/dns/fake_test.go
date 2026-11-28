package dns

import (
	"context"
	"testing"
)

func TestFakeRecords(t *testing.T) {
	var p Provider = &Fake{}
	ctx := context.Background()
	r := Record{ID: "one", Value: "first"}
	if err := p.Create(ctx, "example.com", r); err != nil {
		t.Fatal(err)
	}
	if err := p.Create(ctx, "example.com", r); err == nil {
		t.Fatal("duplicate accepted")
	}
	r.Value = "second"
	if err := p.Update(ctx, "example.com", r); err != nil {
		t.Fatal(err)
	}
	got, err := p.List(ctx, "example.com")
	if err != nil || len(got) != 1 || got[0] != r {
		t.Fatalf("records: %v, %v", got, err)
	}
	if err := p.Delete(ctx, "example.com", r); err != nil {
		t.Fatal(err)
	}
	got, err = p.List(ctx, "example.com")
	if err != nil || len(got) != 0 {
		t.Fatalf("records after delete: %v, %v", got, err)
	}
}

func TestFakeDNSOperations(t *testing.T) {
	f := &Fake{Verified: map[string]bool{"example.com": true}}
	ctx := context.Background()
	if ok, err := f.Verify(ctx, "example.com"); err != nil || !ok {
		t.Fatal("verification failed")
	}
	if ok, err := f.Verify(ctx, "other.example"); err != nil || ok {
		t.Fatal("unexpected verification")
	}
	if err := f.PresentTXT(ctx, "_acme.example.com", "one"); err != nil {
		t.Fatal(err)
	}
	if err := f.PresentTXT(ctx, "_acme.example.com", "two"); err != nil {
		t.Fatal(err)
	}
	if err := f.CleanupTXT(ctx, "_acme.example.com", "one"); err != nil {
		t.Fatal(err)
	}
	if f.TXT["_acme.example.com"]["one"] || !f.TXT["_acme.example.com"]["two"] {
		t.Fatal("cleanup removed wrong value")
	}
	urls := []string{"https://example.com/"}
	if err := f.Purge(ctx, "example.com", urls); err != nil {
		t.Fatal(err)
	}
	urls[0] = "changed"
	if f.Purges["example.com"][0][0] != "https://example.com/" {
		t.Fatal("purge did not copy URLs")
	}
}

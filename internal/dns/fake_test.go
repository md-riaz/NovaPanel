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

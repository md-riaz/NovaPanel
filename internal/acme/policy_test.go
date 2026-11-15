package acme

import (
	"context"
	"errors"
	"fmt"
	"novapanel/internal/store"
	"testing"
	"time"
)

func TestPolicyExactSetAndRegisteredDomain(t *testing.T) {
	s, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := int64(2000000000000)
	p := Policy{Store: s, Now: func() int64 { return now }}
	o := Order{Environment: Staging, Domains: []string{"www.example.co.uk", "example.co.uk"}}
	for i := 0; i < 5; i++ {
		if e = p.BeforeOrder(ctx, o); e != nil {
			t.Fatal(e)
		}
	}
	o.Domains = []string{"example.co.uk", "www.example.co.uk"}
	if e = p.BeforeOrder(ctx, o); !errors.Is(e, store.ErrACMERateLimited) {
		t.Fatal("set order evades limit", e)
	}
	o.Environment = Production
	if e = p.BeforeOrder(ctx, o); e != nil {
		t.Fatal("environment isolation", e)
	}
	for i := 0; i < 45; i++ {
		o = Order{Environment: Staging, Domains: []string{fmt.Sprintf("s%d.example.co.uk", i)}}
		if e = p.BeforeOrder(ctx, o); e != nil {
			t.Fatal(i, e)
		}
	}
	o.Domains = []string{"new.example.co.uk"}
	if e = p.BeforeOrder(ctx, o); !errors.Is(e, store.ErrACMERateLimited) {
		t.Fatal("registered domain budget", e)
	}
	now += int64(7 * 24 * time.Hour / time.Millisecond)
	if e = p.BeforeOrder(ctx, o); e != nil {
		t.Fatal("rollover", e)
	}
}
func TestPolicyAuthFailureAndWildcard(t *testing.T) {
	s, e := store.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	now := int64(2000000000000)
	p := Policy{Store: s, Now: func() int64 { return now }}
	o := Order{Environment: Staging, Domains: []string{"x.example.com"}}
	for i := 0; i < 5; i++ {
		if e = p.FailedOrder(ctx, o); e != nil {
			t.Fatal(e)
		}
	}
	if e = p.BeforeOrder(ctx, o); !errors.Is(e, store.ErrACMERateLimited) {
		t.Fatal("auth failure budget", e)
	}
	now += 3600000
	if e = p.BeforeOrder(ctx, o); e != nil {
		t.Fatal("hour reset", e)
	}
	o.Domains = []string{"*.example.com"}
	if e = p.BeforeOrder(ctx, o); e == nil {
		t.Fatal("wildcard accepted")
	}
}

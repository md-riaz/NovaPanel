package verbs

import (
	"context"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/dns"
	"novapanel/internal/id"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type blockingDNSProvider struct {
	dns.Provider
	entered chan struct{}
}

func (p blockingDNSProvider) List(ctx context.Context, zone string) ([]dns.Record, error) {
	close(p.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestDNSReconcileDoesNotBlockStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal([]byte("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	integrationID, _ := id.New()
	if err = s.CreateIntegration(ctx, integrationID, "{}", store.ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, map[string]string{"example.com": "zone"}); err != nil {
		t.Fatal(err)
	}
	zone, err := s.DNSZoneByName(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	objID, _ := id.New()
	if err = s.ReserveDNSRecord(ctx, store.DNSRecord{ObjID: objID, ZoneID: zone.ID, Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1}); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	a := &agent.Agent{Store: s, Secret: sealer, DNSProvider: func(string, map[string]string) dns.Provider {
		return blockingDNSProvider{Provider: &dns.Fake{}, entered: entered}
	}}
	started := make(chan func(), 1)
	go func() { started <- StartDNSReconcile(ctx, a, nil) }()
	var stop func()
	select {
	case stop = <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup blocked on provider")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		stop()
		t.Fatal("reconcile did not start")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop failed to cancel provider")
	}
}

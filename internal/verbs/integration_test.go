package verbs

import (
	"context"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"testing"
)

func TestIntegrationSetProbeFailure(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a := &agent.Agent{Store: s, Secret: sealer, ProbeDNS: func(context.Context, string, []string) error { return errors.New("token leaked by provider") }}
	args := &integrationSetArgs{Kind: "cloudflare", Token: "token leaked by provider", Zones: []string{"example.com"}}
	if err := args.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err = integrationSet(context.Background(), a, args)
	if err == nil || err.Error() == "token leaked by provider" {
		t.Fatal("probe error was not safely rejected")
	}
	a.ProbeDNS = func(context.Context, string, []string) error { return nil }
	if _, err := integrationSet(context.Background(), a, args); err != nil {
		t.Fatal(err)
	}
}

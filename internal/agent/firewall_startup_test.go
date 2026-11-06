package agent

import (
	"context"
	"errors"
	"novapanel/internal/config"
	"novapanel/internal/validate"
	"testing"
)

func TestFirewallStartupMandatoryConstruction(t *testing.T) {
	for _, a := range []*Agent{New(), NewWithDependencies(config.Defaults(), nil, &validate.V{})} {
		if a.firewallStartup == nil {
			t.Fatal("missing mandatory production preflight")
		}
		if a.FirewallRecovery != nil {
			t.Fatal("legacy recovery enabled")
		}
	}
	if e := (&Agent{}).FirewallStartup(context.Background()); e == nil {
		t.Fatal("zero value permits startup")
	}
	a := New()
	want := errors.New("private preflight fixture")
	a.firewallStartup = func(context.Context) error { return want }
	if e := a.FirewallStartup(context.Background()); !errors.Is(e, want) {
		t.Fatal(e)
	}
}

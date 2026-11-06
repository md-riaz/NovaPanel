package agent

import (
	"context"
	"novapanel/internal/firewall"
	"testing"
)

func TestFirewallStartupLifecycleMissingDependencies(t *testing.T) {
	a := &Agent{}
	if _, e := a.PrepareFirewallStartup(t.Context()); e == nil {
		t.Fatal("zero Agent granted classification")
	}
	if e := a.CompleteFirewallStartup(t.Context(), &firewall.FirewallStartupPlan{}); e == nil {
		t.Fatal("zero plan granted completion")
	}
}

func TestFirewallStartupAdmissionOrder(t *testing.T) {
	// The production factory is the only source of installed plans. Its failure
	// must be returned without opening daemon state, not interpreted as permission.
	a := &Agent{}
	called := false
	a.firewallStartupPlan = func(context.Context) (*firewall.FirewallStartupPlan, error) {
		called = true
		return nil, firewall.ErrFirewallRecoveryPending
	}
	if p, e := a.PrepareFirewallStartup(t.Context()); !called || p != nil || e != firewall.ErrFirewallRecoveryPending {
		t.Fatal(p, e, called)
	}
	if a.Store != nil || a.Audit != nil {
		t.Fatal("classification opened state")
	}
}

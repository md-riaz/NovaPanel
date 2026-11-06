package main

import (
	"context"
	"novapanel/internal/agent"
	"novapanel/internal/config"
	"novapanel/internal/validate"
	"strings"
	"testing"
)

func TestFirewallStartupBeforeOpenState(t *testing.T) {
	cfg := config.Defaults()
	cfg.TLSDir = "relative-startup-sentinel"
	a := agent.NewWithDependencies(cfg, safeRunner{}, &validate.V{})
	e := serve(context.Background(), a)
	if e == nil || !strings.Contains(e.Error(), "firewall startup") {
		t.Fatalf("serve reached state initialization without mandatory firewall admission: %v", e)
	}
	if a.Store != nil || a.Jobs != nil || a.Scheduler != nil {
		t.Fatal("startup initialized state or workers")
	}
}

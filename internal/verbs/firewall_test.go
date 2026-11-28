package verbs

import (
	"novapanel/internal/agent"
	"novapanel/internal/firewall"
	"novapanel/internal/rpc"
	"testing"
)

func TestFirewallVerbContracts(t *testing.T) {
	for _, name := range []string{"firewall.apply", "firewall.confirm", "firewall.revert"} {
		v := rpc.Registry[name]
		if v.MinRole != rpc.Admin || v.Scope != "firewall:write" || !v.MFA {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	v := rpc.Registry["firewall.apply"]
	if !v.Slow || !v.Effectful || v.ObjectKey(nil) != "firewall" {
		t.Fatal("apply attributes")
	}
	if _, err := firewall.Plan([]firewall.Rule{{Action: "deny", Proto: "tcp", Port: 22}}, 22, "127.0.0.1:8080"); err == nil {
		t.Fatal("accepted pinned deny")
	}
	if (&firewallPendingArgs{PendingID: "bad"}).Validate() == nil {
		t.Fatal("accepted invalid pending id")
	}
}

func TestUnknownWebListenPinsPanelPort(t *testing.T) {
	a := agent.New()
	a.Cfg.WebListen = "127.0.0.1:8080"
	a.Cfg.WebListenKnown = false
	plan, err := firewall.Plan(nil, 22, firewallWebListen(a))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, args := range plan {
		if args[len(args)-1] == "8080" {
			found = true
		}
	}
	if !found {
		t.Fatalf("panel port not pinned: %v", plan)
	}
}

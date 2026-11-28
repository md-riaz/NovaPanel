//go:build lab

package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	runner "novapanel/internal/exec"
	"novapanel/internal/firewall"
	"novapanel/internal/id"
	"novapanel/internal/store"
)

// TestFirewallLab must run only in a separately verified network-none container.
func TestFirewallLab(t *testing.T) {
	if os.Getenv("NOVAPANEL_FIREWALL_LAB") != "1" {
		t.Skip("isolated firewall lab not enabled")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("lab requires a Docker container")
	}
	ns, err := os.Readlink("/proc/self/ns/net")
	if err != nil || ns != os.Getenv("NOVAPANEL_FIREWALL_LAB_NETNS") || !strings.HasPrefix(ns, "net:[") {
		t.Fatalf("network namespace not verified: %q %v", ns, err)
	}
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) != 1 || interfaces[0].Name != "lo" {
		t.Fatalf("expected isolated loopback only: %v %v", interfaces, err)
	}
	ctx := context.Background()
	run := func(args ...string) []byte {
		t.Helper()
		out, e := osexec.CommandContext(ctx, "/usr/sbin/ufw", args...).CombinedOutput()
		if e != nil {
			t.Fatalf("ufw %v: %v: %s", args, e, out)
		}
		return out
	}
	run("--force", "reset")
	run("allow", "22/tcp")
	run("--force", "enable")
	before := run("status", "numbered")
	if !bytes.Contains(before, []byte("22/tcp")) {
		t.Fatalf("baseline SSH absent: %s", before)
	}
	root, err := os.MkdirTemp("/var/lib", "novapanel-fw-lab-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	dbPath := filepath.Join(root, "panel.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	pendingID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(root, "snapshot")
	if err = firewall.Snapshot(ctx, runner.Real{}, "/etc/ufw", snap); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveFirewall(ctx, store.FirewallPending{ID: pendingID, SnapshotPath: snap, RevertUnit: "novapanel-fwrevert-" + pendingID + ".timer", Deadline: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = firewall.Plan([]firewall.Rule{{Action: "reset", Proto: "tcp", Port: 22}}, 22, "127.0.0.1:8080"); err == nil {
		t.Fatal("API accepted reset")
	}
	if err = firewall.Apply(ctx, runner.Real{}, []firewall.Rule{{Action: "allow", Proto: "tcp", Port: 34567}}, 22, "127.0.0.1:8080"); err != nil {
		t.Fatal(err)
	}
	enabled, rules, err := firewall.Status(ctx, runner.Real{})
	if err != nil || !enabled || len(rules) == 0 || rules[0].Port != "22" || rules[0].Action != "allow" {
		t.Fatalf("SSH is not first: enabled=%v rules=%+v err=%v", enabled, rules, err)
	}
	after := run("status", "numbered")
	if !bytes.Contains(after, []byte("34567/tcp")) {
		t.Fatalf("requested rule missing: %s", after)
	}
	if err = os.MkdirAll("/etc/novapanel", 0700); err != nil {
		t.Fatal(err)
	}
	configPath := "/etc/novapanel/agent.toml"
	if err = os.WriteFile(configPath, []byte(fmt.Sprintf("db_path = %q\n", dbPath)), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(configPath)
	binary := os.Getenv("NOVAPANEL_FIREWALL_LAB_AGENT")
	if !filepath.IsAbs(binary) {
		t.Fatal("missing lab agent executable")
	}
	out, err := osexec.CommandContext(ctx, binary, "fw-revert", snap).CombinedOutput()
	if err != nil {
		t.Fatalf("fw-revert: %v: %s", err, out)
	}
	restored := run("status", "numbered")
	if !bytes.Equal(before, restored) {
		t.Fatalf("snapshot not restored\nbefore: %s\nafter: %s", before, restored)
	}
	ruleset, err := osexec.CommandContext(ctx, "/usr/sbin/iptables-save").Output()
	if err != nil || !bytes.Contains(ruleset, []byte("--dport 22 -j ACCEPT")) {
		t.Fatalf("SSH accept missing: %v\n%s", err, ruleset)
	}
	s, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.FirewallPending(ctx); err == nil {
		t.Fatal("pending change survived revert")
	}
	if _, err = os.Stat(filepath.Join(root, "fw-reverted-"+pendingID)); err != nil {
		t.Fatal(err)
	}
}

package firewall

import (
	"context"
	"net"
	"novapanel/internal/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type recorder struct {
	calls [][]string
	exit  int
}

func (r *recorder) Run(_ context.Context, path string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{path}, args...))
	if len(args) == 2 && args[0] == "show" && args[1] == "added" {
		return []byte("Added user rules (see UFW):\n"), nil, r.exit, nil
	}
	return nil, nil, r.exit, nil
}

func TestPlanPinsAndValidates(t *testing.T) {
	plan, err := Plan(nil, 2222, ":8443")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 4 || !reflect.DeepEqual(plan[0], []string{"insert", "1", "allow", "in", "proto", "tcp", "from", "any", "to", "any", "port", "2222"}) {
		t.Fatalf("unexpected plan: %v", plan)
	}
	for _, r := range []Rule{{Action: "deny", Proto: "tcp", Port: 2222}, {Action: "reject", Proto: "udp", Port: 443}, {Action: "reset", Proto: "tcp", Port: 123}, {Action: "allow", Proto: "tcp", Port: 23, Source: "bad"}} {
		if _, err := Plan([]Rule{r}, 2222, ":8443"); err == nil {
			t.Fatalf("accepted %#v", r)
		}
	}
}
func TestDryRunOnly(t *testing.T) {
	r := &recorder{}
	if err := DryRun(context.Background(), r, nil, 22, ":8443"); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 6 || r.calls[1][0] != "/usr/sbin/ufw" || r.calls[1][1] != "--dry-run" {
		t.Fatalf("unexpected calls: %v", r.calls)
	}
	r.exit = 1
	if err := DryRun(context.Background(), r, nil, 22, ":8443"); err == nil {
		t.Fatal("accepted failure")
	}
}

func TestWebListenPinsOnlyExternalPorts(t *testing.T) {
	for _, tc := range []struct {
		listen string
		count  int
	}{
		{"127.0.0.1:8080", 3}, {"127.20.1.5:8080", 3}, {"[::1]:8080", 3},
		{":8080", 4}, {"0.0.0.0:8080", 4}, {"[::]:8080", 4},
	} {
		plan, err := Plan(nil, 22, tc.listen)
		if err != nil || len(plan) != tc.count {
			t.Errorf("%s: %v, %v", tc.listen, plan, err)
		}
	}
	for _, listen := range []string{"bad", ":0", ":65536"} {
		if _, err := Plan(nil, 22, listen); err == nil {
			t.Errorf("accepted %q", listen)
		}
	}
	if _, err := Plan(nil, 0, "127.0.0.1:8080"); err == nil {
		t.Fatal("accepted unknown SSH port")
	}
}

func TestApplyUsesPlannedOrder(t *testing.T) {
	r := &recorder{}
	if err := Apply(context.Background(), r, []Rule{{Action: "allow", Proto: "udp", Port: 53}}, 2222, ":8443"); err != nil {
		t.Fatal(err)
	}
	plan, _ := reconcilePlan([]Rule{{Action: "allow", Proto: "udp", Port: 53}}, nil, 2222, ":8443")
	if len(r.calls) != len(plan)+2 {
		t.Fatalf("calls: %v", r.calls)
	}
	for i, args := range plan {
		if !reflect.DeepEqual(r.calls[i+1], append([]string{"/usr/sbin/ufw"}, args...)) {
			t.Fatalf("call %d: %v", i, r.calls[i+1])
		}
	}
	if !reflect.DeepEqual(r.calls[len(plan)+1], []string{"/usr/sbin/ufw", "--force", "enable"}) {
		t.Fatal(r.calls)
	}
}

func TestReconcileRemovesOmittedRules(t *testing.T) {
	old := []Rule{{Action: "allow", Proto: "tcp", Port: 22, Comment: "existing ssh"}, {Action: "allow", Proto: "tcp", Port: 3306}, {Action: "deny", Proto: "tcp", Port: 1234}}
	plan, err := reconcilePlan(nil, old, 22, "0.0.0.0:8443")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, args := range plan {
		if len(args) > 0 {
			seen[args[len(args)-1]] = true
		}
	}
	// The existing unrestricted SSH allow is kept, so no pinned SSH allow is added beside it.
	for _, port := range []string{"80", "443", "8443"} {
		if !seen[port] {
			t.Fatalf("missing pinned port %s: %v", port, plan)
		}
	}
	if seen["22"] {
		t.Fatalf("existing SSH allow was touched: %v", plan)
	}
	var deletes [][]string
	for _, args := range plan {
		if len(args) > 1 && args[1] == "delete" {
			deletes = append(deletes, args)
		}
	}
	if len(deletes) != 2 || deletes[0][len(deletes[0])-1] != "3306" || deletes[1][len(deletes[1])-1] != "1234" {
		t.Fatalf("deletions: %v", deletes)
	}
}

func TestReconcileKeepsSourceRestrictedPinnedRules(t *testing.T) {
	for _, port := range []int{22, 80, 443, 8443} {
		restricted := Rule{Action: "allow", Proto: "tcp", Port: port, Source: "192.0.2.0/24"}
		plan, err := reconcilePlan(nil, []Rule{restricted}, 22, "0.0.0.0:8443")
		if err != nil {
			t.Fatal(err)
		}
		want := strconv.Itoa(port)
		for _, args := range plan {
			if args[len(args)-1] == want {
				t.Fatalf("restricted port %d was touched or widened: %v", port, args)
			}
		}
	}
}

type restrictedSSHRunner struct{}

func (restrictedSSHRunner) Run(context.Context, string, []string, exec.RunOpts) ([]byte, []byte, int, error) {
	return []byte("Added user rules (see 'ufw status' for running firewall):\nufw allow from 192.0.2.0/24 to any port 22 proto tcp\n"), nil, 0, nil
}

func TestReconcileKeepsExistingSourceRestrictedSSH(t *testing.T) {
	old, err := existingRules(context.Background(), restrictedSSHRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 1 || old[0].Source != "192.0.2.0/24" || old[0].Port != 22 {
		t.Fatalf("parsed rules: %+v", old)
	}
	plan, err := reconcilePlan(nil, old, 22, "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range plan {
		if args[len(args)-1] == "22" {
			t.Fatalf("restricted SSH rule deleted or replaced: %v", plan)
		}
		if len(args) > 0 && args[0] == "insert" {
			t.Fatalf("unrestricted SSH allow inserted beside restricted rule: %v", plan)
		}
	}
	seen := map[string]bool{}
	for _, args := range plan {
		seen[args[len(args)-1]] = true
	}
	if !seen["80"] || !seen["443"] {
		t.Fatalf("unrelated pinned ports missing: %v", plan)
	}
}

func TestReconcileInsertsPinnedSSHWhenAbsent(t *testing.T) {
	plan, err := reconcilePlan(nil, nil, 22, "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"insert", "1", "allow", "in", "proto", "tcp", "from", "any", "to", "any", "port", "22"}
	if len(plan) == 0 || !reflect.DeepEqual(plan[0], want) {
		t.Fatalf("SSH not inserted first: %v", plan)
	}
}

type addedRunner struct{}

func (addedRunner) Run(context.Context, string, []string, exec.RunOpts) ([]byte, []byte, int, error) {
	return []byte("Added user rules (see 'ufw status' for running firewall):\nufw allow 3306\nufw allow 22/tcp\n"), nil, 0, nil
}
func TestReconcileRemovesProtocolAgnosticPort(t *testing.T) {
	old, err := existingRules(context.Background(), addedRunner{})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := reconcilePlan(nil, old, 22, "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, args := range plan {
		if reflect.DeepEqual(args, []string{"--force", "delete", "allow", "3306"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing deletion: %v", plan)
	}
}

// simRule is one rule in the order ufw would hold it.
type simRule struct {
	action, source, proto string
	port                  int
}

// simApply applies one ruleArgs-form command the way ufw does: insert N places the rule at position N,
// a plain rule appends, and --force delete removes the first identical rule. Other commands are ignored.
func simApply(rules []simRule, args []string) []simRule {
	pos := -1
	if len(args) > 2 && args[0] == "insert" {
		pos, _ = strconv.Atoi(args[1])
		args = args[2:]
	}
	del := false
	if len(args) > 2 && args[0] == "--force" && args[1] == "delete" {
		del = true
		args = args[2:]
	}
	if len(args) < 10 || args[1] != "in" || args[6] != "to" {
		return rules
	}
	port, _ := strconv.Atoi(args[9])
	r := simRule{action: args[0], proto: args[3], source: args[5], port: port}
	if del {
		for i, x := range rules {
			if x == r {
				return append(rules[:i:i], rules[i+1:]...)
			}
		}
		return rules
	}
	if pos >= 1 && pos <= len(rules) {
		out := append([]simRule{}, rules[:pos-1]...)
		out = append(out, r)
		return append(out, rules[pos-1:]...)
	}
	return append(rules, r)
}

// simVerdict returns the action of the first rule matching a TCP connection, as ufw would decide.
func simVerdict(rules []simRule, src string, port int) string {
	ip := net.ParseIP(src)
	for _, r := range rules {
		if r.port != port || r.proto != "tcp" {
			continue
		}
		if r.source != "any" {
			if strings.Contains(r.source, "/") {
				_, n, _ := net.ParseCIDR(r.source)
				if n == nil || !n.Contains(ip) {
					continue
				}
			} else if net.ParseIP(r.source).String() != ip.String() {
				continue
			}
		}
		return r.action
	}
	return "default"
}

// simState converts existing rules into the order ufw would hold them.
func simState(old []Rule) []simRule {
	var out []simRule
	for _, r := range old {
		src := r.Source
		if src == "" {
			src = "any"
		}
		out = append(out, simRule{action: r.Action, source: src, proto: r.Proto, port: r.Port})
	}
	return out
}

// Source-restricted deny and reject rules on a pinned port are explicit admin choices: they must survive
// reconcile, and the world-open pinned allow must sit after them so the blocked source stays blocked.
func TestReconcileKeepsSourceDenyOnPinnedPort(t *testing.T) {
	for _, action := range []string{"deny", "reject"} {
		for _, port := range []int{22, 80, 443} {
			old := []Rule{{Action: action, Proto: "tcp", Port: port, Source: "1.2.3.4"}}
			plan, err := reconcilePlan(nil, old, 22, "127.0.0.1:8080")
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range plan {
				if len(args) > 2 && args[0] == "--force" && strings.Contains(strings.Join(args, " "), "from 1.2.3.4") {
					t.Fatalf("%s from 1.2.3.4 on %d deleted: %v", action, port, plan)
				}
				if args[0] == "insert" && args[len(args)-1] == strconv.Itoa(port) {
					t.Fatalf("world allow inserted ahead of %s on %d: %v", action, port, plan)
				}
			}
			final := simState(old)
			for _, args := range plan {
				final = simApply(final, args)
			}
			if got := simVerdict(final, "1.2.3.4", port); got != action {
				t.Fatalf("blocked source got %q after apply on %d: %v", got, port, plan)
			}
			if got := simVerdict(final, "9.9.9.9", port); got != "allow" {
				t.Fatalf("other source got %q on %d: %v", got, port, plan)
			}
		}
	}
}

// A source-restricted limit is kept as well, and it covers the pinned port so no world allow is added.
func TestReconcileKeepsSourceLimitOnPinnedPort(t *testing.T) {
	old := []Rule{{Action: "limit", Proto: "tcp", Port: 22, Source: "10.0.0.0/8"}}
	plan, err := reconcilePlan(nil, old, 22, "127.0.0.1:8080")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range plan {
		if len(args) > 0 && args[0] == "--force" {
			t.Fatalf("limit deleted: %v", plan)
		}
		if args[len(args)-1] == "22" {
			t.Fatalf("pinned SSH widened beside limit: %v", plan)
		}
	}
	final := simState(old)
	for _, args := range plan {
		final = simApply(final, args)
	}
	if len(final) != 1 || final[0].action != "limit" {
		t.Fatalf("limit not preserved: %+v", final)
	}
}

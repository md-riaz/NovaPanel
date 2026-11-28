// Package firewall plans structured UFW rules without executing shell commands.
package firewall

import (
	"context"
	"net"
	"novapanel/internal/exec"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"strconv"
)

type Rule struct {
	Action  string `json:"action"`
	Proto   string `json:"proto"`
	Port    int    `json:"port"`
	Source  string `json:"source,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// Plan validates the complete requested set and prepends the required TCP allows.
func Plan(rules []Rule, sshPort int, webListen string) ([][]string, error) {
	v := &validate.V{}
	host, service, err := net.SplitHostPort(webListen)
	if err != nil {
		return nil, invalid("web listen address")
	}
	panelPort, err := net.LookupPort("tcp", service)
	if err != nil || v.Port(panelPort) != nil {
		return nil, invalid("web listen port")
	}
	pinned := []int{sshPort, 80, 443}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		pinned = append(pinned, panelPort)
	}
	seen := make(map[int]bool)
	plan := make([][]string, 0, len(rules)+4)
	for _, port := range pinned {
		if err := v.Port(port); err != nil {
			return nil, err
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		args := ruleArgs(Rule{Action: "allow", Proto: "tcp", Port: port})
		if port == sshPort {
			args = append([]string{"insert", "1"}, args...)
		}
		plan = append(plan, args)
	}
	if len(rules) > 256 {
		return nil, invalid("rule count")
	}
	for _, r := range rules {
		if r.Action != "allow" && r.Action != "deny" && r.Action != "reject" && r.Action != "limit" {
			return nil, invalid("action")
		}
		if r.Proto != "tcp" && r.Proto != "udp" {
			return nil, invalid("protocol")
		}
		if r.Action == "limit" && r.Proto != "tcp" {
			return nil, invalid("limit protocol")
		}
		if err := v.Port(r.Port); err != nil {
			return nil, err
		}
		if seen[r.Port] && r.Action != "allow" {
			return nil, invalid("pinned port")
		}
		if r.Source != "" {
			if err := v.CIDR(r.Source); err != nil {
				return nil, err
			}
		}
		if len(r.Comment) > 128 {
			return nil, invalid("comment")
		}
		for _, c := range r.Comment {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '-' || c == '_' || c == '.') {
				return nil, invalid("comment")
			}
		}
		plan = append(plan, ruleArgs(r))
	}
	return plan, nil
}
func invalid(field string) error {
	return rpcerr.Errf("invalid_argument", "invalid firewall %s", field)
}
func ruleArgs(r Rule) []string {
	source := r.Source
	if source == "" {
		source = "any"
	}
	args := []string{r.Action, "in", "proto", r.Proto, "from", source, "to", "any", "port", strconv.Itoa(r.Port)}
	if r.Comment != "" {
		args = append(args, "comment", r.Comment)
	}
	return args
}

func DryRun(ctx context.Context, runner exec.Runner, rules []Rule, sshPort int, webListen string) error {
	if _, err := Plan(rules, sshPort, webListen); err != nil {
		return err
	}
	if runner == nil {
		return rpcerr.Errf("agent_error", "firewall runner missing")
	}
	old, err := existingRules(ctx, runner)
	if err != nil {
		return err
	}
	plan, err := reconcilePlan(rules, old, sshPort, webListen)
	if err != nil {
		return err
	}
	for _, args := range plan {
		_, _, code, err := runner.Run(ctx, "/usr/sbin/ufw", append([]string{"--dry-run"}, args...), exec.RunOpts{})
		if err != nil || code != 0 {
			return rpcerr.Errf("agent_error", "firewall dry-run failed")
		}
	}
	return nil
}

// Apply executes the validated plan in order, then enables UFW.
func Apply(ctx context.Context, runner exec.Runner, rules []Rule, sshPort int, webListen string) error {
	if _, err := Plan(rules, sshPort, webListen); err != nil {
		return err
	}
	if runner == nil {
		return rpcerr.Errf("agent_error", "firewall runner missing")
	}
	old, err := existingRules(ctx, runner)
	if err != nil {
		return err
	}
	plan, err := reconcilePlan(rules, old, sshPort, webListen)
	if err != nil {
		return err
	}
	for _, args := range plan {
		if _, err = firewallCommand(ctx, runner, "/usr/sbin/ufw", args...); err != nil {
			return err
		}
	}
	_, err = firewallCommand(ctx, runner, "/usr/sbin/ufw", "--force", "enable")
	return err
}

package firewall

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"novapanel/internal/exec"
)

var shortAdded = regexp.MustCompile(`^(allow|deny|reject|limit) ([0-9]+)(?:/(tcp|udp))?(?: comment '([A-Za-z0-9 ._-]*)')?$`)
var sourceAdded = regexp.MustCompile(`^(allow|deny|reject|limit) from ([0-9A-Fa-f:./]+) to any port ([0-9]+) proto (tcp|udp)(?: comment '([A-Za-z0-9 ._-]*)')?$`)

// existingRules only accepts the subset of UFW rules expressible by the API.
func existingRules(ctx context.Context, runner exec.Runner) ([]Rule, error) {
	out, err := firewallCommand(ctx, runner, "/usr/sbin/ufw", "show", "added")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "Added user rules ") {
		return nil, errors.New("unrecognized UFW rules")
	}
	rules := []Rule{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "ufw ") {
			return nil, errors.New("unrecognized UFW rule")
		}
		line = strings.TrimPrefix(line, "ufw ")
		var rule Rule
		if m := shortAdded.FindStringSubmatch(line); m != nil {
			rule.Action = m[1]
			rule.Port, _ = strconv.Atoi(m[2])
			rule.Proto = m[3]
			rule.Comment = m[4]
		} else if m := sourceAdded.FindStringSubmatch(line); m != nil {
			rule.Action = m[1]
			rule.Source = m[2]
			rule.Port, _ = strconv.Atoi(m[3])
			rule.Proto = m[4]
			rule.Comment = m[5]
		} else {
			return nil, errors.New("unsupported existing UFW rule")
		}
		validationRule := rule
		validationRule.Action = "allow"
		if validationRule.Proto == "" {
			validationRule.Proto = "tcp"
		}
		if _, err = Plan([]Rule{validationRule}, 1, "127.0.0.1:8080"); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func reconcilePlan(rules, old []Rule, sshPort int, webListen string) ([][]string, error) {
	plan, err := Plan(rules, sshPort, webListen)
	if err != nil {
		return nil, err
	}
	pinned, err := Plan(nil, sshPort, webListen)
	if err != nil {
		return nil, err
	}
	desired := make(map[Rule]bool)
	for _, r := range rules {
		desired[r] = true
	}
	for _, args := range pinned {
		port, _ := strconv.Atoi(args[len(args)-1])
		desired[Rule{Action: "allow", Proto: "tcp", Port: port}] = true
	}
	current := make(map[Rule]bool)
	for _, r := range old {
		current[r] = true
	}
	full := append([][]string{}, pinned...)
	full = append(full, []string{"default", "deny", "incoming"})
	for i, r := range rules {
		if !current[r] {
			full = append(full, plan[len(pinned)+i])
		}
	}
	for _, r := range old {
		if desired[r] {
			continue
		}
		if r.Proto == "" && r.Source == "" {
			args := []string{"--force", "delete", r.Action, strconv.Itoa(r.Port)}
			if r.Comment != "" {
				args = append(args, "comment", r.Comment)
			}
			full = append(full, args)
		} else {
			full = append(full, append([]string{"--force", "delete"}, ruleArgs(r)...))
		}
	}
	return full, nil
}

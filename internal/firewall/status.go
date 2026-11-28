package firewall

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"novapanel/internal/exec"
)

type StatusRule struct {
	Num     int    `json:"num"`
	Action  string `json:"action"`
	Proto   string `json:"proto"`
	Port    string `json:"port"`
	Source  string `json:"source"`
	Comment string `json:"comment"`
}

var statusRule = regexp.MustCompile(`^\[\s*([0-9]+)\]\s+(\S+)(?:\s+\(v6\))?\s+(ALLOW|DENY|REJECT|LIMIT)\s+(?:IN|OUT|FWD)\s+(.+)$`)

func Status(ctx context.Context, runner exec.Runner) (bool, []StatusRule, error) {
	if runner == nil {
		return false, nil, errors.New("missing firewall runner")
	}
	out, err := firewallCommand(ctx, runner, "/usr/sbin/ufw", "status", "numbered")
	if err != nil {
		return false, nil, err
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) == 0 {
		return false, nil, errors.New("invalid firewall status")
	}
	first := strings.TrimSpace(lines[0])
	if first == "Status: inactive" {
		return false, []StatusRule{}, nil
	}
	if first != "Status: active" {
		return false, nil, errors.New("invalid firewall status")
	}
	rules := []StatusRule{}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		m := statusRule.FindStringSubmatch(line)
		if m == nil {
			return false, nil, errors.New("unrecognized firewall rule")
		}
		n, _ := strconv.Atoi(m[1])
		port, proto, _ := strings.Cut(m[2], "/")
		source, comment, _ := strings.Cut(m[4], " # ")
		rules = append(rules, StatusRule{Num: n, Action: strings.ToLower(m[3]), Proto: proto, Port: port, Source: strings.TrimSpace(source), Comment: comment})
	}
	return true, rules, nil
}

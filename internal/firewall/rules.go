// Package firewall provides structured UFW planning and injected recovery seams.
package firewall

import (
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
type Desired struct {
	Rules           []Rule `json:"rules"`
	DefaultIncoming string `json:"default_incoming,omitempty"`
}

// Render returns argv suffixes, never command strings. The consumer must replace
// the prior desired ruleset, not append these rules to an unknown live ruleset.
func Render(d Desired, sshPort int, sshSource string) ([][]string, error) {
	v := &validate.V{}
	if err := v.Port(sshPort); err != nil {
		return nil, err
	}
	if sshSource != "" {
		if err := v.CIDR(sshSource); err != nil {
			return nil, err
		}
	}
	policy := d.DefaultIncoming
	if policy == "" {
		policy = "deny"
	}
	if policy != "allow" && policy != "deny" && policy != "reject" {
		return nil, invalid("default incoming")
	}
	if len(d.Rules) > 256 {
		return nil, invalid("rule count")
	}
	ssh := Rule{Action: "allow", Proto: "tcp", Port: sshPort, Source: sshSource}
	out := [][]string{append([]string{"insert", "1"}, ruleArgs(ssh)...), {"default", policy, "incoming"}}
	for _, r := range d.Rules {
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
		out = append(out, ruleArgs(r))
	}
	return out, nil
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

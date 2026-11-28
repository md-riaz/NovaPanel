package firewall

import (
	"context"
	"novapanel/internal/exec"
	"testing"
)

type statusRunner struct{}

func (statusRunner) Run(context.Context, string, []string, exec.RunOpts) ([]byte, []byte, int, error) {
	return []byte("Status: active\n\n[ 1] 22/tcp ALLOW IN Anywhere\n[ 2] 443/tcp (v6) ALLOW IN Anywhere (v6) # web\n"), nil, 0, nil
}
func TestStatus(t *testing.T) {
	enabled, rules, err := Status(context.Background(), statusRunner{})
	if err != nil || !enabled || len(rules) != 2 || rules[0].Port != "22" || rules[1].Comment != "web" {
		t.Fatalf("%v %#v %v", enabled, rules, err)
	}
}

package firewall

import (
	"context"
	"novapanel/internal/exec"
	"reflect"
	"testing"
)

type recorder struct {
	calls [][]string
	exit  int
}

func (r *recorder) Run(_ context.Context, path string, args []string, _ exec.RunOpts) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, append([]string{path}, args...))
	return nil, nil, r.exit, nil
}

func TestPlanPinsAndValidates(t *testing.T) {
	plan, err := Plan(nil, 2222, 8443)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 4 || !reflect.DeepEqual(plan[0], []string{"insert", "1", "allow", "in", "proto", "tcp", "from", "any", "to", "any", "port", "2222"}) {
		t.Fatalf("unexpected plan: %v", plan)
	}
	for _, r := range []Rule{{Action: "deny", Proto: "tcp", Port: 2222}, {Action: "reject", Proto: "udp", Port: 443}, {Action: "reset", Proto: "tcp", Port: 123}, {Action: "allow", Proto: "tcp", Port: 23, Source: "bad"}} {
		if _, err := Plan([]Rule{r}, 2222, 8443); err == nil {
			t.Fatalf("accepted %#v", r)
		}
	}
}
func TestDryRunOnly(t *testing.T) {
	r := &recorder{}
	if err := DryRun(context.Background(), r, nil, 22, 8443); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 4 || r.calls[0][0] != "/usr/sbin/ufw" || r.calls[0][1] != "--dry-run" {
		t.Fatalf("unexpected calls: %v", r.calls)
	}
	r.exit = 1
	if err := DryRun(context.Background(), r, nil, 22, 8443); err == nil {
		t.Fatal("accepted failure")
	}
}

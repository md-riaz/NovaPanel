package firewall

import (
	"context"
	"testing"
)

func TestDryRunValidatesWholePlanBeforeRunner(t *testing.T) {
	r := &recordingRunner{}
	err := DryRun(context.Background(), r, Desired{Rules: []Rule{{Action: "allow", Proto: "tcp", Port: 80}, {Action: "allow;reset", Proto: "tcp", Port: 443}}}, 22, "")
	if err == nil || len(r.calls) != 0 {
		t.Fatalf("invalid desired state reached runner: %v %v", err, r.calls)
	}
	if err := DryRun(context.Background(), r, Desired{}, 22, ""); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 || r.calls[0][0] != "/usr/sbin/ufw" || r.calls[0][1] != "--dry-run" {
		t.Fatal(r.calls)
	}
	r.fail = true
	if err := DryRun(context.Background(), r, Desired{}, 22, ""); err == nil {
		t.Fatal("nonzero exit accepted")
	}
}

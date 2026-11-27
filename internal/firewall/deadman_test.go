package firewall

import (
	"context"
	"testing"
)

func TestDeadmanCommands(t *testing.T) {
	r := &recorder{}
	const pending = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	unit, err := Arm(context.Background(), r, pending, "/var/lib/novapanel/firewall/snapshot", 15)
	if err != nil || unit != "novapanel-fwrevert-"+pending+".timer" {
		t.Fatalf("%q %v", unit, err)
	}
	if len(r.calls) != 1 || r.calls[0][0] != "/usr/bin/systemd-run" || r.calls[0][2] != "--on-active=15min" {
		t.Fatal(r.calls)
	}
	if err := Cancel(context.Background(), r, pending); err != nil {
		t.Fatal(err)
	}
	if r.calls[1][0] != "/usr/bin/systemctl" || r.calls[1][2] != unit {
		t.Fatal(r.calls)
	}
	if _, err := Arm(context.Background(), r, "bad", "/tmp/snapshot", 15); err == nil {
		t.Fatal("invalid ID accepted")
	}
	r.exit = 1
	if _, err := Arm(context.Background(), r, pending, "/tmp/snapshot", 15); err == nil {
		t.Fatal("failed timer accepted")
	}
}

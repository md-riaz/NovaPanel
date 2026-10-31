package integration

import (
	"os"
	"strings"
	"testing"
)

func TestPackagingRecoveryLayout(t *testing.T) {
	b, e := os.ReadFile("../../packaging/tmpfiles/novapanel.conf")
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range []string{
		"d /var/lib/novapanel/firewall 0700 root root -",
		"d /var/lib/novapanel/firewall/journal 0700 root root -",
		"d /var/lib/novapanel/firewall/snapshots 0700 root root -",
		"d /var/lib/novapanel/firewall/restore 0700 root root -",
		"d /var/lib/novapanel/firewall/restore/markers 0700 root root -",
		"f /var/lib/novapanel/firewall/journal/lock 0600 root root -",
	} {
		if !strings.Contains(string(b), line+"\n") {
			t.Error("missing", line)
		}
	}
	if strings.Contains(string(b), "f+ /var/lib/novapanel/firewall") || strings.Contains(string(b), "F /var/lib/novapanel/firewall") {
		t.Fatal("truncating lock provisioning")
	}
}

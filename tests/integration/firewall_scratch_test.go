package integration

import (
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/firewall"
)

func firewallDispatcherScratch(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "fw-dispatch-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	return root
}

// Root execution must not turn a runner-owned checkout into trusted custody.
// Keep the process TMPDIR under the host-approved scratch ancestry throughout.
func TestFirewallDispatcherScratchAncestry(t *testing.T) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		t.Skip("fixed runtime metadata requires root UID/GID fixtures")
	}
	checkout := t.TempDir()
	if err := os.Chown(checkout, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	t.Chdir(checkout)
	root := firewallDispatcherScratch(t)
	if filepath.Dir(root) == checkout {
		t.Fatal("custody fixture allocated in runner-owned checkout")
	}
	c, err := firewall.OpenSnapshotCustody(root)
	if err != nil {
		t.Fatalf("trusted scratch under foreign checkout: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// An unsafe configured scratch path must still fail admission. Do not
	// change its ownership, weaken ancestor checks, or silently fall back.
	t.Setenv("TMPDIR", checkout)
	unsafe := firewallDispatcherScratch(t)
	if c, err := firewall.OpenSnapshotCustody(unsafe); err == nil {
		c.Close()
		t.Fatal("runner-owned scratch ancestor admitted")
	}
}

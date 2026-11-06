package firewall

import (
	"os"
	"path/filepath"
	"testing"
)

// Exercise the launcher's scratch contract as both the ordinary CI user and root.
// Neither a foreign checkout nor a writable temporary ancestor is safe custody.
func TestSnapshotCustodyScratchAncestry(t *testing.T) {
	checkout := t.TempDir()
	if os.Geteuid() == 0 {
		if err := os.Chown(checkout, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(checkout)
	root := custodyTempDir(t)
	if filepath.Dir(root) == checkout {
		t.Fatal("custody fixture allocated in checkout")
	}
	c, err := OpenSnapshotCustody(root)
	if err != nil {
		t.Fatalf("launcher scratch under unrelated checkout: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	unsafe := t.TempDir()
	if err := os.Chmod(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", unsafe)
	if c, err := OpenSnapshotCustody(custodyTempDir(t)); err == nil {
		c.Close()
		t.Fatal("writable scratch ancestor admitted")
	}
}

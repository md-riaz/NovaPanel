package configcustody

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The injected root writer deliberately violates exclusion to distinguish which
// inode is authenticated. Production still requires root-writer exclusion.
func TestTerminalCleanupAuthenticatesHeldDescriptor(t *testing.T) {
	b, dir, seal := configFixture(t)
	owner := uint32(os.Geteuid())
	ops := configOps{fixtureAncestors: true}
	if err := publishConfig(context.Background(), dir, b, []byte("private"), seal, owner, ops); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, b.JobID+".sealed")
	authentic, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, []byte("unauthenticated held inode"), 0600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	ops.boundary = func(stage string) error {
		switch stage {
		case "cleanup-read":
			swapped = true
			if err := os.Rename(name, name+".held"); err != nil {
				return err
			}
			return os.WriteFile(name, authentic, 0600)
		case "cleanup-unlink":
			if err := os.Remove(name); err != nil {
				return err
			}
			return os.Rename(name+".held", name)
		}
		return nil
	}
	err = removeConfigRecord(context.Background(), dir, b, seal, owner, ops)
	if !swapped {
		t.Fatal("read boundary not exercised")
	}
	if err == nil {
		t.Fatal("authenticated a second inode and removed unauthenticated held inode")
	}
	if got, err := os.ReadFile(name + ".held"); err != nil || string(got) != "unauthenticated held inode" {
		t.Fatal("held record was changed or removed", err)
	}
	if got, err := os.ReadFile(filepath.Join(b.Docroot, "wp-config.php")); err != nil || string(got) != "private" {
		t.Fatal("live configuration changed", err)
	}
}

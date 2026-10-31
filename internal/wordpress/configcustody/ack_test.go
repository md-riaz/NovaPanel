package configcustody

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcknowledgedRecordRequiresSyncedAbsence(t *testing.T) {
	b, dir, seal := configFixture(t)
	owner := uint32(os.Geteuid())
	ops := configOps{fixtureAncestors: true}
	if err := publishConfig(context.Background(), dir, b, []byte("private"), seal, owner, ops); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(dir, b.JobID+".sealed")
	for _, kind := range []string{"authentic", "unmanaged", "symlink"} {
		if kind != "authentic" {
			if err := os.Remove(record); err != nil {
				t.Fatal(err)
			}
			if kind == "unmanaged" {
				if err := os.WriteFile(record, []byte("unmanaged"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink("missing", record); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := checkConfigRecordAbsent(context.Background(), dir, b.JobID, owner, ops); err == nil {
			t.Fatal("accepted present", kind)
		}
		if _, err := os.Lstat(record); err != nil {
			t.Fatal("removed conflict", err)
		}
	}
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	synced := false
	ops.boundary = func(stage string) error {
		if stage == "cleanup-dir-sync" {
			synced = true
			return errors.New("injected")
		}
		return nil
	}
	if err := checkConfigRecordAbsent(context.Background(), dir, b.JobID, owner, ops); err == nil || !synced {
		t.Fatal("absence did not require sync", err)
	}
	if err := checkConfigRecordAbsent(context.Background(), dir, b.JobID, owner, configOps{fixtureAncestors: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(b.Docroot, "wp-config.php")); err != nil || string(got) != "private" {
		t.Fatal("live config changed", err)
	}
}

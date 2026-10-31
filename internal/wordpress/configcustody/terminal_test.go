package configcustody

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalRecordCleanupOwnsOnlyAuthenticatedInode(t *testing.T) {
	for _, kind := range []string{"success", "binding", "symlink", "hardlink", "rebound", "cancel", "sync"} {
		t.Run(kind, func(t *testing.T) {
			b, dir, seal := configFixture(t)
			owner := uint32(os.Geteuid())
			ops := configOps{fixtureAncestors: true}
			if err := publishConfig(context.Background(), dir, b, []byte("private"), seal, owner, ops); err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(dir, b.JobID+".sealed")
			original, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "binding":
				b.SiteID = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
			case "symlink":
				if err = os.Rename(record, record+".saved"); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(record+".saved", record); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err = os.Link(record, record+".saved"); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			case "rebound":
				ops.boundary = func(stage string) error {
					if stage == "cleanup-unlink" {
						if e := os.Rename(record, record+".saved"); e != nil {
							return e
						}
						return os.WriteFile(record, original, 0600)
					}
					return nil
				}
			case "sync":
				ops.boundary = func(stage string) error {
					if stage == "cleanup-dir-sync" {
						return errors.New("injected")
					}
					return nil
				}
			}
			err = removeConfigRecord(ctx, dir, b, seal, owner, ops)
			if kind == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(record); !os.IsNotExist(err) {
					t.Fatal("record retained", err)
				}
				if err = removeConfigRecord(ctx, dir, b, seal, owner, ops); err != nil {
					t.Fatal("retry", err)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe cleanup succeeded")
				}
				if kind != "sync" {
					if _, err = os.Lstat(record); err != nil {
						t.Fatal("removed refused record", err)
					}
				} else if err = removeConfigRecord(context.Background(), dir, b, seal, owner, configOps{fixtureAncestors: true}); err != nil {
					t.Fatal("sync retry", err)
				}
			}
			if got, e := os.ReadFile(filepath.Join(b.Docroot, "wp-config.php")); e != nil || string(got) != "private" {
				t.Fatal("live config changed", e)
			}
		})
	}
}

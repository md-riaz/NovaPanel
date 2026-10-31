package configcustody

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/secret"
)

func configFixture(t *testing.T) (ConfigBinding, string, *secret.Sealer) {
	t.Helper()
	base := t.TempDir()
	custody := filepath.Join(base, "custody")
	home := filepath.Join(base, "site")
	docroot := filepath.Join(home, "public")
	for _, p := range []string{custody, home, docroot} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(home, 0751); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(docroot, os.ModeSetgid|0750); err != nil {
		t.Fatal(err)
	}
	seal, err := secret.New(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return ConfigBinding{JobID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", DatabaseID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", AccountID: "01ARZ3NDEKTSV4RRFFQ69G5FAY", Docroot: docroot, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}, custody, seal
}

func TestProtectedConfigPublishesAndReopensSameIdentity(t *testing.T) {
	b, dir, seal := configFixture(t)
	plain := []byte("<?php\ndefine('DB_PASSWORD', 'private-password');\ndefine('AUTH_KEY', 'private-salt');\n")
	for range 2 {
		if err := publishConfig(context.Background(), dir, b, plain, seal, uint32(os.Geteuid()), configOps{fixtureAncestors: true}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(b.Docroot, "wp-config.php"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("config mismatch", err)
	}
	info, err := os.Stat(filepath.Join(b.Docroot, "wp-config.php"))
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("config mode", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("custody entries: %d", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private-password")) || bytes.Contains(raw, []byte("private-salt")) {
		t.Fatal("plaintext in recovery record")
	}
}

func TestProtectedConfigFailureBoundariesReopen(t *testing.T) {
	for _, stage := range []string{"record-write", "record-sync", "record-close", "record-rename", "record-dir-sync", "config-write", "config-sync", "config-close", "config-rename", "target-dir-sync", "custody-dir-sync"} {
		t.Run(stage, func(t *testing.T) {
			b, dir, seal := configFixture(t)
			plain := []byte("<?php /* private deterministic fixture */")
			injected := errors.New("secret-shaped failure must not escape")
			ops := configOps{fixtureAncestors: true, boundary: func(at string) error {
				if at == stage {
					return injected
				}
				return nil
			}}
			err := publishConfig(context.Background(), dir, b, plain, seal, uint32(os.Geteuid()), ops)
			if err != errConfigCustody {
				t.Fatalf("unsanitized or missing failure: %v", err)
			}
			if err = publishConfig(context.Background(), dir, b, plain, seal, uint32(os.Geteuid()), configOps{fixtureAncestors: true}); err != nil {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}

func TestProtectedConfigCancellationAfterRenameReopens(t *testing.T) {
	b, dir, seal := configFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ops := configOps{fixtureAncestors: true, boundary: func(stage string) error {
		if stage == "target-dir-sync" {
			cancel()
			return context.Canceled
		}
		return nil
	}}
	if err := publishConfig(ctx, dir, b, []byte("private"), seal, uint32(os.Geteuid()), ops); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := publishConfig(context.Background(), dir, b, []byte("private"), seal, uint32(os.Geteuid()), configOps{fixtureAncestors: true}); err != nil {
		t.Fatal(err)
	}
}

func TestProtectedConfigRefusesUnmanagedAndTamperedFiles(t *testing.T) {
	for _, kind := range []string{"existing", "symlink", "hardlink", "mode", "changed", "identity", "bytes", "ancestor", "record-symlink", "temp-symlink"} {
		t.Run(kind, func(t *testing.T) {
			b, dir, seal := configFixture(t)
			plain := []byte("protected-password-and-salts")
			path := filepath.Join(b.Docroot, "wp-config.php")
			run := func() error {
				return publishConfig(context.Background(), dir, b, plain, seal, uint32(os.Geteuid()), configOps{fixtureAncestors: true})
			}
			switch kind {
			case "existing":
				if e := os.WriteFile(path, plain, 0400); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Symlink("missing", path); e != nil {
					t.Fatal(e)
				}
			case "ancestor":
				moved := b.Docroot + "-real"
				if e := os.Rename(b.Docroot, moved); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(moved, b.Docroot); e != nil {
					t.Fatal(e)
				}
			case "record-symlink":
				if e := os.Symlink("missing", filepath.Join(dir, b.JobID+".sealed")); e != nil {
					t.Fatal(e)
				}
			case "temp-symlink":
				if e := os.Symlink("missing", filepath.Join(dir, b.JobID+".sealed.tmp")); e != nil {
					t.Fatal(e)
				}
			default:
				if e := run(); e != nil {
					t.Fatal(e)
				}
				switch kind {
				case "hardlink":
					if e := os.Link(path, filepath.Join(dir, "alias")); e != nil {
						t.Fatal(e)
					}
				case "mode":
					if e := os.Chmod(path, 0600); e != nil {
						t.Fatal(e)
					}
				case "changed":
					if e := os.Chmod(path, 0600); e != nil {
						t.Fatal(e)
					}
					if e := os.WriteFile(path, []byte("changed"), 0600); e != nil {
						t.Fatal(e)
					}
					if e := os.Chmod(path, 0400); e != nil {
						t.Fatal(e)
					}
				case "identity":
					b.SiteID = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
				case "bytes":
					plain = []byte("different")
				}
			}
			if e := run(); e != errConfigCustody {
				t.Fatalf("expected refusal, got %v", e)
			}
		})
	}
}

func TestProtectedConfigPublicEntryRejectsWritableAncestor(t *testing.T) {
	b, dir, seal := configFixture(t)
	// Even under root, the production API must reject group/world-writable
	// ancestors. The private fixture policy is not available through it.
	if err := os.Chmod(filepath.Dir(dir), 0777); err != nil {
		t.Fatal(err)
	}
	b.UID = 12345
	b.GID = 12345
	if err := PublishConfig(context.Background(), dir, b, []byte("private"), seal); err != errConfigCustody {
		t.Fatal(err)
	}
}

func TestProtectedConfigRecoversInterruptedPrivateTempWrite(t *testing.T) {
	b, dir, seal := configFixture(t)
	stop := configOps{fixtureAncestors: true, boundary: func(stage string) error {
		if stage == "config-write" {
			return errors.New("stop")
		}
		return nil
	}}
	if e := publishConfig(context.Background(), dir, b, []byte("complete private bytes"), seal, uint32(os.Geteuid()), stop); e != errConfigCustody {
		t.Fatal(e)
	}
	// Simulate process death during a write, after the sealed record was synced.
	if e := os.WriteFile(filepath.Join(dir, b.JobID+".config"), []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := publishConfig(context.Background(), dir, b, []byte("complete private bytes"), seal, uint32(os.Geteuid()), configOps{fixtureAncestors: true}); e != nil {
		t.Fatal(e)
	}
}

func TestProtectedConfigRenameNeverOverwritesConcurrentDestination(t *testing.T) {
	b, dir, seal := configFixture(t)
	path := filepath.Join(b.Docroot, "wp-config.php")
	ops := configOps{fixtureAncestors: true, boundary: func(stage string) error {
		if stage == "config-rename" {
			return os.WriteFile(path, []byte("unmanaged"), 0400)
		}
		return nil
	}}
	if e := publishConfig(context.Background(), dir, b, []byte("private"), seal, uint32(os.Geteuid()), ops); e != errConfigCustody {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(path)
	if e != nil || string(raw) != "unmanaged" {
		t.Fatal("overwritten", e)
	}
	if e = publishConfig(context.Background(), dir, b, []byte("private"), seal, uint32(os.Geteuid()), configOps{fixtureAncestors: true}); e != errConfigCustody {
		t.Fatal(e)
	}
}

package configcustody

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/secret"
)

// Invalid identities must be rejected before cancellation or filesystem access.
func TestSentinelIdentityRejectedBeforeIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	seal, err := secret.New(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"uid", "gid"} {
		b := ConfigBinding{JobID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", DatabaseID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", AccountID: "01ARZ3NDEKTSV4RRFFQ69G5FAY", UID: 1001, GID: 1001}
		if field == "uid" {
			b.UID = ^uint32(0)
		} else {
			b.GID = ^uint32(0)
		}
		called := false
		if err := PublishConfig(ctx, "unused", b, []byte("private"), seal); err != errConfigCustody {
			t.Errorf("%s publish: %v", field, err)
		}
		if err := RemoveConfigRecord(ctx, "unused", b, seal); err != errConfigCustody {
			t.Errorf("%s remove: %v", field, err)
		}
		if err := RemoveConfigRecordAuthorized(ctx, "unused", b, seal, func() error { called = true; return nil }); err != errConfigCustody || called {
			t.Errorf("%s authorized remove: %v, called=%v", field, err, called)
		}
	}
}

// Public operations require root-owned, non-writable ancestors, unlike scratch fixtures.
func TestPublicBoundariesRejectAllOnesIdentity(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("public custody fixtures require root")
	}
	base, err := os.MkdirTemp("/root", "config-custody-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	custody := filepath.Join(base, "custody")
	home := filepath.Join(base, "site")
	docroot := filepath.Join(home, "public")
	for _, p := range []string{custody, home, docroot} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(home, 1001, 1001); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(docroot, 1001, 1001); err != nil {
		t.Fatal(err)
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
	baseBinding := ConfigBinding{JobID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAW", DatabaseID: "01ARZ3NDEKTSV4RRFFQ69G5FAX", AccountID: "01ARZ3NDEKTSV4RRFFQ69G5FAY", Docroot: docroot, UID: 1001, GID: 1001}
	for _, field := range []string{"uid", "gid", "both"} {
		t.Run(field, func(t *testing.T) {
			b := baseBinding
			if field != "gid" {
				b.UID = ^uint32(0)
			}
			if field != "uid" {
				b.GID = ^uint32(0)
			}
			ctx := context.Background()
			if err := PublishConfig(ctx, custody, b, []byte("private"), seal); err != errConfigCustody {
				t.Fatalf("publish: %v", err)
			}
			entries, err := os.ReadDir(custody)
			if err != nil || len(entries) != 0 {
				t.Fatalf("publish changed custody: %v, %v", entries, err)
			}
			if _, err := os.Lstat(filepath.Join(docroot, "wp-config.php")); !os.IsNotExist(err) {
				t.Fatalf("publish changed docroot: %v", err)
			}
			plain, err := json.Marshal(configRecord{Version: 1, Binding: b, Config: []byte("private")})
			if err != nil {
				t.Fatal(err)
			}
			envelope, err := seal.Seal(plain)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			record := filepath.Join(custody, b.JobID+".sealed")
			if err := os.WriteFile(record, raw, 0600); err != nil {
				t.Fatal(err)
			}
			for _, authorized := range []bool{false, true} {
				called := false
				var err error
				if authorized {
					err = RemoveConfigRecordAuthorized(ctx, custody, b, seal, func() error { called = true; return nil })
				} else {
					err = RemoveConfigRecord(ctx, custody, b, seal)
				}
				if err != errConfigCustody || called {
					t.Errorf("cleanup authorized=%v: err=%v callback=%v", authorized, err, called)
				}
				got, e := os.ReadFile(record)
				if e != nil || !bytes.Equal(got, raw) {
					t.Fatalf("cleanup changed record: %v", e)
				}
			}
			if err := os.Remove(record); err != nil {
				t.Fatal(err)
			}
			called := false
			if err := RemoveConfigRecordAuthorized(ctx, custody, b, seal, func() error { called = true; return nil }); err != errConfigCustody || called {
				t.Errorf("absent cleanup: err=%v callback=%v", err, called)
			}
		})
	}
}

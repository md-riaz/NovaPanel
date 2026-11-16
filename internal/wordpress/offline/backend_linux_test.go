//go:build wordpress_offline_lab && linux

package offline

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
	"novapanel/internal/php"
)

// account creates a disposable site account and returns its uid and gid.
func account(t *testing.T, ctx context.Context, r runner.Runner, root, name, id string) (uint32, uint32) {
	t.Helper()
	if err := php.EnsureUser(ctx, r, root, name, id); err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	out, _, code, err := r.Run(ctx, "/usr/bin/getent", []string{"passwd", name}, runner.RunOpts{})
	if err != nil || code != 0 {
		t.Fatalf("getent passwd: %v (code=%d)", err, code)
	}
	f := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(f) != 7 {
		t.Fatalf("invalid getent output")
	}
	uid, err := strconv.ParseUint(f[2], 10, 32)
	if err != nil {
		t.Fatalf("parse uid: %v", err)
	}
	gid, err := strconv.ParseUint(f[3], 10, 32)
	if err != nil {
		t.Fatalf("parse gid: %v", err)
	}
	return uint32(uid), uint32(gid)
}

// emptyDocroot makes a fresh empty directory owned by uid:gid under root. The
// site home is unusable as a docroot because useradd -m seeds it from /etc/skel.
func emptyDocroot(t *testing.T, root, leaf string, uid, gid uint32) string {
	t.Helper()
	dir := filepath.Join(root, leaf)
	if err := os.Mkdir(dir, 0750); err != nil {
		t.Fatalf("mkdir docroot: %v", err)
	}
	if err := os.Chown(dir, int(uid), int(gid)); err != nil {
		t.Fatalf("chown docroot: %v", err)
	}
	return dir
}

// TestLabRestrictedInstall exercises a full offline install into a fresh,
// site-UID-owned docroot and the retain-and-quarantine teardown.
func TestLabRestrictedInstall(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires an explicitly disposable root container")
	}
	ctx := context.Background()
	r := runner.Real{}
	root := t.TempDir()
	if err := os.Chmod(filepath.Dir(root), 0755); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatalf("chmod root: %v", err)
	}

	name := fmt.Sprintf("npwp%d", os.Getpid())
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	uid, gid := account(t, ctx, r, root, name, id)
	docroot := emptyDocroot(t, root, "docroot", uid, gid)

	lt := NewLabLifetime(uid, gid, name, id, docroot)
	cfg := []byte("<?php // test wp-config\n")
	if err := InstallWordPress(lt, cfg); err != nil {
		t.Fatalf("InstallWordPress: %v", err)
	}

	var st unix.Stat_t
	if err := unix.Lstat(filepath.Join(docroot, "wp-config.php"), &st); err != nil {
		t.Fatalf("lstat wp-config.php: %v", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		t.Fatalf("wp-config.php is not a regular file")
	}
	if st.Uid != uid {
		t.Fatalf("wp-config.php uid mismatch: got %d, want %d", st.Uid, uid)
	}
	if st.Mode&0777 != 0400 {
		t.Fatalf("wp-config.php perms mismatch: got %o, want 0400", st.Mode&0777)
	}

	// A known top-level file from the WordPress archive must be present and
	// owned by the site uid.
	if err := unix.Lstat(filepath.Join(docroot, "index.php"), &st); err != nil {
		t.Fatalf("lstat index.php: %v", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uid {
		t.Fatalf("index.php ownership/type mismatch: uid=%d mode=%o", st.Uid, st.Mode)
	}

	// Teardown retains and quarantines the account; the UID is never freed.
	if err := php.RetainUser(ctx, r, root, name, id); err != nil {
		t.Fatalf("RetainUser: %v", err)
	}
	out, _, code, err := r.Run(ctx, "/usr/bin/getent", []string{"passwd", name}, runner.RunOpts{})
	if err != nil || code != 0 {
		t.Fatalf("getent after retain: %v (code=%d)", err, code)
	}
	f := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(f) != 7 || !strings.HasPrefix(f[4], "novapanel-retained=") {
		t.Fatalf("account not retained, GECOS=%q", f[4])
	}
	if got, err := strconv.ParseUint(f[2], 10, 32); err != nil || uint32(got) != uid {
		t.Fatalf("retained account UID changed: got %q want %d", f[2], uid)
	}
}

// TestLabRestrictedRefusals asserts every refusal path fails closed.
func TestLabRestrictedRefusals(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires an explicitly disposable root container")
	}
	ctx := context.Background()
	r := runner.Real{}
	root := t.TempDir()
	if err := os.Chmod(filepath.Dir(root), 0755); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatalf("chmod root: %v", err)
	}

	name := fmt.Sprintf("npwpref%d", os.Getpid())
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	uid, gid := account(t, ctx, r, root, name, id)
	cfg := []byte("<?php // config\n")

	tests := []struct {
		name string
		fn   func() error
	}{
		{"no established handle", func() error {
			return InstallWordPress(ProtectedLifetime{}, cfg)
		}},
		{"empty config", func() error {
			d := emptyDocroot(t, root, "cfgless", uid, gid)
			return InstallWordPress(NewLabLifetime(uid, gid, name, id, d), nil)
		}},
		{"nonempty docroot", func() error {
			d := emptyDocroot(t, root, "nonempty", uid, gid)
			if err := os.WriteFile(filepath.Join(d, "existing.txt"), []byte("x"), 0644); err != nil {
				t.Fatalf("seed docroot: %v", err)
			}
			return InstallWordPress(NewLabLifetime(uid, gid, name, id, d), cfg)
		}},
		{"foreign-owned docroot", func() error {
			d := filepath.Join(root, "foreign")
			if err := os.Mkdir(d, 0750); err != nil {
				t.Fatalf("mkdir foreign: %v", err)
			}
			// Left owned by root; the handle claims the site uid.
			return InstallWordPress(NewLabLifetime(uid, gid, name, id, d), cfg)
		}},
		{"relative docroot", func() error {
			return InstallWordPress(NewLabLifetime(uid, gid, name, id, "relative/path"), cfg)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, errRefused) {
				t.Fatalf("expected errRefused, got %v", err)
			}
		})
	}
}

// buildZip assembles a one-entry zip archive for the extraction guard tests.
func buildZip(t *testing.T, name string, mode os.FileMode, data []byte) *zip.Reader {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: name, Method: zip.Deflate}
	fh.SetMode(mode)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		t.Fatalf("create zip header %q: %v", name, err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write zip data %q: %v", name, err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	return zr
}

// TestExtractRejectsMaliciousEntries verifies the per-entry guards without the
// 37 MiB archive. A symlink, a traversal, an absolute path, and an out-of-tree
// entry must each fail closed; a benign regular entry must extract.
func TestExtractRejectsMaliciousEntries(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires an explicitly disposable root container")
	}
	dir := t.TempDir()
	uid := uint32(os.Getuid())
	gid := uint32(os.Getgid())

	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatalf("open test dir: %v", err)
	}
	defer unix.Close(dfd)
	var ds unix.Stat_t
	if err := unix.Fstat(dfd, &ds); err != nil {
		t.Fatalf("fstat: %v", err)
	}
	dev := ds.Dev

	tests := []struct {
		name    string
		entry   string
		mode    os.FileMode
		data    []byte
		wantErr bool
	}{
		{"traversal", "wordpress/../evil", 0644, nil, true},
		{"absolute", "/etc/evil", 0644, nil, true},
		{"outside prefix", "notwp/x", 0644, nil, true},
		{"symlink", "wordpress/evil.txt", os.ModeSymlink | 0777, []byte("/etc/passwd"), true},
		{"benign", "wordpress/ok.txt", 0644, []byte("content"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			zr := buildZip(t, tc.entry, tc.mode, tc.data)
			err := extract(dfd, zr, uid, gid, dev)
			if tc.wantErr {
				if !errors.Is(err, errRefused) {
					t.Fatalf("expected errRefused, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			var st unix.Stat_t
			if err := unix.Fstatat(dfd, "ok.txt", &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				t.Fatalf("benign entry not written: %v", err)
			}
			if st.Mode&unix.S_IFMT != unix.S_IFREG {
				t.Fatalf("benign entry is not a regular file")
			}
		})
	}
}

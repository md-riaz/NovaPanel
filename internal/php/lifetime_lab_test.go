//go:build wordpress_offline_lab && linux

package php

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
)

// This creates an account and intentionally retains it: run only in a disposable container.
func TestLabRetainedAccountLifetime(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires an explicitly disposable root container")
	}
	ctx := context.Background()
	r := runner.Real{}
	root := t.TempDir()
	if err := os.Chmod(filepath.Dir(root), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("nplab%d", os.Getpid())
	id := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if err := EnsureUser(ctx, r, root, name, id); err != nil {
		t.Fatal(err)
	}
	lookup := func() []string {
		out, _, code, err := r.Run(ctx, "/usr/bin/getent", []string{"passwd", name}, runner.RunOpts{})
		if err != nil || code != 0 {
			t.Fatalf("account lookup: %v", err)
		}
		f := strings.Split(strings.TrimSpace(string(out)), ":")
		if len(f) != 7 {
			t.Fatal("invalid account")
		}
		return f
	}
	f := lookup()
	uid, err := strconv.ParseUint(f[2], 10, 32)
	if err != nil || f[4] != marker+id {
		t.Fatal("account provenance missing")
	}
	var st unix.Stat_t
	if err := unix.Lstat(filepath.Join(root, name), &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || uint64(st.Uid) != uid {
		t.Fatal("home ownership mismatch")
	}
	gid, err := strconv.ParseUint(f[3], 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, name, "owned")
	_, _, code, err := r.Run(ctx, "/usr/bin/touch", []string{file}, runner.RunOpts{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}})
	if err != nil || code != 0 {
		t.Fatalf("site-UID write: %v", err)
	}
	if err := unix.Lstat(file, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || uint64(st.Uid) != uid {
		t.Fatal("file ownership mismatch")
	}
	if err := os.Chmod(file, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, name), 0751); err != nil {
		t.Fatal(err)
	}
	other := fmt.Sprintf("npother%d", os.Getpid())
	otherID := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	if err := EnsureUser(ctx, r, root, other, otherID); err != nil {
		t.Fatal(err)
	}
	out, _, code, err := r.Run(ctx, "/usr/bin/getent", []string{"passwd", other}, runner.RunOpts{})
	if err != nil || code != 0 {
		t.Fatal("second account lookup")
	}
	f2 := strings.Split(strings.TrimSpace(string(out)), ":")
	if len(f2) != 7 {
		t.Fatal("invalid second account")
	}
	otherUID, e1 := strconv.ParseUint(f2[2], 10, 32)
	otherGID, e2 := strconv.ParseUint(f2[3], 10, 32)
	if e1 != nil || e2 != nil || otherUID == uid {
		t.Fatal("UIDs are not distinct")
	}
	_, _, code, err = r.Run(ctx, "/usr/bin/cat", []string{file}, runner.RunOpts{Uid: uint32(otherUID), Gid: uint32(otherGID), Groups: []uint32{}})
	if err == nil || code == 0 {
		t.Fatal("second site read private file")
	}
	if err := RetainUser(ctx, r, root, other, otherID); err != nil {
		t.Fatal(err)
	}
	if err := RetainUser(ctx, r, root, name, id); err != nil {
		t.Fatal(err)
	}
	f = lookup()
	if f[4] != retainedMarker+id || f[2] != strconv.FormatUint(uid, 10) {
		t.Fatal("retirement did not retain the original UID")
	}
	if err := EnsureUser(ctx, r, root, name, id); err == nil {
		t.Fatal("retired account was re-adopted")
	}
}

//go:build lab

package fileop

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
	runner "novapanel/internal/exec"
)

func TestDroppedArchive(t *testing.T) {
	if os.Getenv("NOVAPANEL_ARCHIVE_CHILD") == "1" {
		if ArchiveHelper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires disposable root lab")
	}
	base, err := os.MkdirTemp("/tmp", "np-archive-lab-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	if err = os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "site", "public")
	if err = os.MkdirAll(filepath.Join(dir, "tree"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, filepath.Join(dir, "tree")} {
		if err = os.Chown(p, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(dir, "tree", "file"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(base, "test")
	if err = os.WriteFile(exe, binary, 0755); err != nil {
		t.Fatal(err)
	}
	call := func(req ArchiveRequest, want string) {
		t.Helper()
		req.Base, req.Name = base, "site"
		data, _ := json.Marshal(req)
		out, _, code, err := (runner.Real{}).Run(context.Background(), exe, []string{"-test.run=^TestDroppedArchive$"}, runner.RunOpts{Uid: 65534, Gid: 65534, Groups: []uint32{}, Env: []string{"NOVAPANEL_ARCHIVE_CHILD=1"}, Stdin: bytes.NewReader(data)})
		var res Response
		if err != nil || code != 0 || json.Unmarshal(out, &res) != nil || res.Code != want {
			t.Fatalf("helper %s: %s %d %v", req.Operation, out, code, err)
		}
	}
	call(ArchiveRequest{Operation: "archive", Paths: []string{"tree"}, Dest: "out.zip"}, "")
	call(ArchiveRequest{Operation: "extract", Archive: "out.zip", Dest: "out"}, "")
	call(ArchiveRequest{Operation: "extract", Archive: "out.zip", Dest: "out"}, "conflict")
	got, err := os.ReadFile(filepath.Join(dir, "out", "tree", "file"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("contents %q %v", got, err)
	}
	for _, p := range []string{"out.zip", "out/tree/file"} {
		var st unix.Stat_t
		if err = unix.Stat(filepath.Join(dir, p), &st); err != nil || st.Uid != 65534 || st.Mode&0777 != 0644 {
			t.Fatalf("ownership %s: %+v %v", p, st, err)
		}
	}
}

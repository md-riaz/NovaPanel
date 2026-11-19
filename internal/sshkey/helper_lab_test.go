//go:build lab

package sshkey

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"testing"

	runner "novapanel/internal/exec"
)

func TestDroppedWrite(t *testing.T) {
	if os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" || os.Geteuid() != 0 {
		t.Skip("disposable root lab required")
	}
	home, err := os.MkdirTemp("", "sshkey-lab-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	const uid = 65534
	const gid = 65534
	if err = os.Chown(home, uid, gid); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	copyPath := filepath.Join(home, "helper-test")
	copyFile, err := os.OpenFile(copyPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(copyFile, source); err != nil {
		t.Fatal(err)
	}
	if err = copyFile.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(Request{Home: home, SiteID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Operation: "add", Value: testKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, code, err := (runner.Real{}).Run(context.Background(), copyPath, []string{"-test.run=^TestDroppedChild$"}, runner.RunOpts{Uid: uid, Gid: gid, Groups: []uint32{}, Env: []string{"SSHKEY_TEST_CHILD=1"}, Stdin: bytes.NewReader(request)})
	if err != nil || code != 0 {
		t.Fatalf("child failed %d %v %s %s", code, err, out, stderr)
	}
	info, err := os.Stat(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Sys().(*unix.Stat_t).Uid != uid || info.Mode().Perm() != 0600 {
		t.Fatalf("incorrect ownership or mode: %v", info)
	}
}
func TestDroppedChild(t *testing.T) {
	if os.Getenv("SSHKEY_TEST_CHILD") != "1" {
		return
	}
	if err := Helper(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

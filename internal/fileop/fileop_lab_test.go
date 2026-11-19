//go:build lab

package fileop

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	runner "novapanel/internal/exec"
)

func TestDroppedHelper(t *testing.T) {
	if os.Getenv("NOVAPANEL_FILEOP_CHILD") == "1" {
		if err := Helper(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires disposable root lab")
	}
	base, err := os.MkdirTemp("/tmp", "novapanel-fileop-lab-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "site", "public")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(base, "test")
	if err = os.WriteFile(binaryPath, binary, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "allowed"), []byte("site-only"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(filepath.Join(dir, "allowed"), 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "denied"), []byte("root-only"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, content, code string }{{"allowed", "site-only", ""}, {"denied", "", "agent_error"}} {
		input, _ := json.Marshal(Request{Base: base, Name: "site", Operation: "read", Path: tc.path})
		out, _, code, err := (runner.Real{}).Run(context.Background(), binaryPath, []string{"-test.run=^TestDroppedHelper$"}, runner.RunOpts{Uid: 65534, Gid: 65534, Groups: []uint32{}, Env: []string{"NOVAPANEL_FILEOP_CHILD=1"}, Stdin: bytes.NewReader(input)})
		if err != nil || code != 0 {
			t.Fatalf("helper: %d %v", code, err)
		}
		var r Response
		if err = json.Unmarshal(out, &r); err != nil || string(r.Content) != tc.content || r.Code != tc.code {
			t.Fatalf("%s: %s %v", tc.path, out, err)
		}
	}
}

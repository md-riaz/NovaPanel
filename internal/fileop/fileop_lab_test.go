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
		input = append(input, '\n')
		out, _, code, err := (runner.Real{}).Run(context.Background(), binaryPath, []string{"-test.run=^TestDroppedHelper$"}, runner.RunOpts{Uid: 65534, Gid: 65534, Groups: []uint32{}, Env: []string{"NOVAPANEL_FILEOP_CHILD=1"}, Stdin: bytes.NewReader(input)})
		if err != nil || code != 0 {
			t.Fatalf("helper: %d %v", code, err)
		}
		var r Response
		if err = json.Unmarshal(out, &r); err != nil || string(r.Content) != tc.content || r.Code != tc.code {
			t.Fatalf("%s: %s %v", tc.path, out, err)
		}
	}
	if err := os.Chown(dir, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	content := []byte("<>&\n\x00byte-exact")
	header, _ := json.Marshal(Request{Base: base, Name: "site", Operation: "write", Path: "written", Length: len(content)})
	header = append(header, '\n')
	input := append(header, content...)
	out, _, code, err := (runner.Real{}).Run(context.Background(), binaryPath, []string{"-test.run=^TestDroppedHelper$"}, runner.RunOpts{Uid: 65534, Gid: 65534, Groups: []uint32{}, Env: []string{"NOVAPANEL_FILEOP_CHILD=1"}, Stdin: bytes.NewReader(input)})
	var response Response
	if err != nil || code != 0 || json.Unmarshal(out, &response) != nil || response.Code != "" {
		t.Fatalf("write helper: %d %s %v", code, out, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "written"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("content %q: %v", got, err)
	}
	info, err := os.Stat(filepath.Join(dir, "written"))
	var owner unix.Stat_t
	if err != nil || unix.Stat(filepath.Join(dir, "written"), &owner) != nil || info.Mode().Perm() != 0644 || owner.Uid != 65534 {
		t.Fatalf("ownership/mode: %v %v", info, err)
	}
	for _, tc := range []struct {
		req     Request
		content []byte
	}{
		{Request{Base: base, Name: "site", Operation: "write", Path: "large", Length: MaxWrite}, bytes.Repeat([]byte{'x'}, MaxWrite)},
		{Request{Base: base, Name: "site", Operation: "mkdir", Path: "created"}, nil},
		{Request{Base: base, Name: "site", Operation: "chmod", Path: "written", Mode: 0600}, nil},
	} {
		header, _ := json.Marshal(tc.req)
		input := append(append(header, '\n'), tc.content...)
		out, _, code, err := (runner.Real{}).Run(context.Background(), binaryPath, []string{"-test.run=^TestDroppedHelper$"}, runner.RunOpts{Uid: 65534, Gid: 65534, Groups: []uint32{}, Env: []string{"NOVAPANEL_FILEOP_CHILD=1"}, Stdin: bytes.NewReader(input)})
		var response Response
		if err != nil || code != 0 || json.Unmarshal(out, &response) != nil || response.Code != "" {
			t.Fatalf("%s: %d %s %v", tc.req.Operation, code, out, err)
		}
	}
	got, err = os.ReadFile(filepath.Join(dir, "large"))
	if err != nil || !bytes.Equal(got, bytes.Repeat([]byte{'x'}, MaxWrite)) {
		t.Fatalf("maximum framed write: len=%d err=%v", len(got), err)
	}
	info, err = os.Stat(filepath.Join(dir, "created"))
	if err != nil || unix.Stat(filepath.Join(dir, "created"), &owner) != nil || !info.IsDir() || owner.Uid != 65534 {
		t.Fatalf("directory ownership: %v %v", info, err)
	}
	info, err = os.Stat(filepath.Join(dir, "written"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("chmod: %v %v", info, err)
	}
}

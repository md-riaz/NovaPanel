package fileop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteOperations(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "site", "public")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	req := Request{Base: base, Name: "site"}
	run := func(op, p, to string, mode uint32, content []byte, want string) {
		t.Helper()
		req.Operation, req.Path, req.From, req.To, req.Mode = op, p, p, to, mode
		_, err := apply(req, content)
		if want == "" && err != nil || want != "" && (err == nil || errorCode(err) != want) {
			t.Fatalf("%s %q -> %q: %v, want %s", op, p, to, err, want)
		}
	}
	run("write", "file", "", 0, []byte("first"), "")
	before, _ := os.Stat(filepath.Join(dir, "file"))
	run("write", "file", "", 0, []byte("second"), "")
	after, _ := os.Stat(filepath.Join(dir, "file"))
	got, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(got) != "second" || os.SameFile(before, after) || after.Mode().Perm() != 0644 {
		t.Fatalf("overwrite: %q %v", got, err)
	}
	run("write", "max", "", 0, bytes.Repeat([]byte{'x'}, MaxWrite), "")
	run("write", "too-large", "", 0, make([]byte, MaxWrite+1), "invalid_argument")
	run("mkdir", "sub", "", 0, nil, "")
	run("mkdir", "sub", "", 0, nil, "conflict")
	run("write", "sub", "", 0, nil, "invalid_argument")
	run("move", "file", "max", 0, nil, "conflict")
	run("move", "file", "sub/moved", 0, nil, "")
	for _, mode := range []uint32{0644, 0640, 0600, 0755, 0750, 0700} {
		run("chmod", "sub/moved", "", mode, nil, "")
		i, err := os.Stat(filepath.Join(dir, "sub", "moved"))
		if err != nil || uint32(i.Mode().Perm()) != mode {
			t.Fatalf("mode %o: %v %v", mode, i, err)
		}
	}
	run("chmod", "sub/moved", "", 0777, nil, "invalid_argument")
	run("delete", "sub", "", 0, nil, "conflict")
	run("delete", "sub/moved", "", 0, nil, "")
	run("delete", "sub", "", 0, nil, "")
	run("delete", "missing", "", 0, nil, "not_found")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "target"), []byte("safe"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"write", "mkdir", "move", "delete", "chmod"} {
		for _, bad := range []string{"../bad", "/tmp/bad", "bad\x00name", "bad\\name", "escape/target"} {
			run(op, bad, "destination", 0644, []byte("bad"), "invalid_argument")
		}
		if op != "delete" {
			run(op, "link", "destination", 0644, []byte("bad"), "invalid_argument")
		}
	}
	for _, bad := range []string{"../bad", "/tmp/bad", "bad\x00name", "bad\\name", "escape/target"} {
		run("move", "max", bad, 0, nil, "invalid_argument")
	}
	run("move", "max", "link", 0, nil, "conflict")
	run("delete", "link", "", 0, nil, "")
	got, err = os.ReadFile(filepath.Join(outside, "target"))
	if err != nil || string(got) != "safe" {
		t.Fatalf("outside changed: %q %v", got, err)
	}
}

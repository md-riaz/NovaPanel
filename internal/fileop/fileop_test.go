package fileop

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadAndConfinement(t *testing.T) {
	base := t.TempDir()
	site := filepath.Join(base, "site")
	root := filepath.Join(site, "public")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("a"), MaxChunk+3)
	if err := os.WriteFile(filepath.Join(root, "file"), data, 0640); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	req := Request{Base: base, Name: "site", Operation: "read", Path: "file", Length: MaxChunk + 1}
	r, err := Apply(req)
	if err != nil || len(r.Content) != MaxChunk || r.Size != int64(len(data)) || r.EOF {
		t.Fatalf("read: %+v %v", r, err)
	}
	req.Offset = MaxChunk
	req.Length = 8
	r, err = Apply(req)
	if err != nil || string(r.Content) != "aaa" || !r.EOF {
		t.Fatalf("tail: %+v %v", r, err)
	}
	req.Offset = len(data) + 10
	r, err = Apply(req)
	if err != nil || len(r.Content) != 0 || !r.EOF {
		t.Fatalf("past EOF: %+v %v", r, err)
	}
	for _, p := range []string{"../", "../../etc/passwd", "/etc/passwd", "bad\x00name", "bad\\name", "link", "link/secret"} {
		req.Path = p
		if _, err = Apply(req); err == nil || errorCode(err) != "invalid_argument" {
			t.Errorf("accepted or misclassified %q: %v", p, err)
		}
	}
	req = Request{Base: base, Name: "site", Operation: "list"}
	r, err = Apply(req)
	if err != nil || len(r.Entries) != 2 {
		t.Fatalf("list: %+v %v", r, err)
	}
	for _, e := range r.Entries {
		if e.Name == "file" && (e.Type != "file" || e.Mode != "0640" || e.Size != int64(len(data))) {
			t.Errorf("file: %+v", e)
		}
		if e.Name == "link" && e.Type != "symlink" {
			t.Errorf("link: %+v", e)
		}
	}
	req.Path = "file"
	if _, err = Apply(req); !errors.Is(err, unix.EINVAL) {
		t.Fatalf("list file: %v", err)
	}
	req.Path = "missing"
	if _, err = Apply(req); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if err = os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenDocroot(base, "site"); err == nil {
		t.Fatal("accepted symlink docroot")
	}
}

func FuzzValidatePath(f *testing.F) {
	for _, p := range []string{"../", "../../etc/passwd", "/etc/passwd", "a\x00b", "a\\b"} {
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if (strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00")) && ValidatePath(p) == nil {
			t.Fatalf("accepted %q", p)
		}
	})
}

func TestEscapeCodes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "site", "public")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"read", "list"} {
		for _, p := range []string{"escape", "escape/secret"} {
			result, err := Apply(Request{Base: base, Name: "site", Operation: op, Path: p})
			if err == nil || errorCode(err) != "invalid_argument" || len(result.Content) != 0 || len(result.Entries) != 0 {
				t.Fatalf("%s %s: %+v, %v", op, p, result, err)
			}
		}
	}
	if errorCode(unix.EIO) != "agent_error" {
		t.Fatal("I/O failure misclassified")
	}
}

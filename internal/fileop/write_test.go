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
	for _, mode := range []os.FileMode{0600, 0640, 0644, os.ModeSetgid | 0640} {
		if err := os.Chmod(filepath.Join(dir, "file"), mode); err != nil {
			t.Fatal(err)
		}
		run("write", "file", "", 0, []byte("private"), "")
		info, err := os.Stat(filepath.Join(dir, "file"))
		got, readErr := os.ReadFile(filepath.Join(dir, "file"))
		if err != nil || readErr != nil || info.Mode()&(os.ModePerm|os.ModeSetgid) != mode || string(got) != "private" {
			t.Fatalf("overwrite mode %o: %v, %v", mode, info, err)
		}
	}
	run("write", "max", "", 0, bytes.Repeat([]byte{'x'}, MaxWrite), "")
	run("write", "too-large", "", 0, make([]byte, MaxWrite+1), "invalid_argument")
	run("mkdir", "sub", "", 0, nil, "")
	run("mkdir", "sub", "", 0, nil, "conflict")
	run("write", "sub", "", 0, nil, "invalid_argument")
	run("move", "file", "max", 0, nil, "conflict")
	before, err = os.Stat(filepath.Join(dir, "file"))
	if err != nil {
		t.Fatal(err)
	}
	run("move", "file", "sub/moved", 0, nil, "")
	after, err = os.Stat(filepath.Join(dir, "sub", "moved"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("move did not preserve inode: %v", err)
	}
	if _, err = os.Lstat(filepath.Join(dir, "file")); !os.IsNotExist(err) {
		t.Fatalf("source survived move: %v", err)
	}
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
		if op != "delete" && op != "move" {
			run(op, "link", "destination", 0644, []byte("bad"), "invalid_argument")
		}
	}
	for _, bad := range []string{"../bad", "/tmp/bad", "bad\x00name", "bad\\name", "escape/target"} {
		run("move", "max", bad, 0, nil, "invalid_argument")
	}
	run("move", "max", "link", 0, nil, "conflict")
	run("move", "missing", "destination", 0, nil, "not_found")
	run("move", "link", "relocated", 0, nil, "")
	if i, err := os.Lstat(filepath.Join(dir, "relocated")); err != nil || i.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("relocated symlink: %v %v", i, err)
	}
	run("read", "relocated", "", 0, nil, "invalid_argument")
	run("write", "relocated", "", 0, []byte("bad"), "invalid_argument")
	run("delete", "relocated", "", 0, nil, "")
	got, err = os.ReadFile(filepath.Join(outside, "target"))
	if err != nil || string(got) != "safe" {
		t.Fatalf("outside changed: %q %v", got, err)
	}
}

func TestMoveConcurrentSymlinkSwap(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "site", "public")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(outside, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	src, staged, dest := filepath.Join(dir, "source"), filepath.Join(dir, "staged"), filepath.Join(dir, "dest")
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if err := os.Symlink(outside, staged); err != nil {
				done <- err
				return
			}
			if err := os.Rename(staged, src); err != nil {
				done <- err
				return
			}
			if err := os.WriteFile(staged, []byte("inside"), 0600); err != nil {
				done <- err
				return
			}
			if err := os.Rename(staged, src); err != nil {
				done <- err
				return
			}
		}
	}()
	for i := 0; i < 500; i++ {
		_, err := Apply(Request{Base: base, Name: "site", Operation: "move", From: "source", To: "dest"})
		if err != nil && !os.IsNotExist(err) {
			close(stop)
			<-done
			t.Fatalf("move: %v", err)
		}
		if err == nil {
			info, e := os.Lstat(dest)
			if e != nil {
				close(stop)
				<-done
				t.Fatalf("destination: %v %v", info, e)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				link, e := os.Readlink(dest)
				if e != nil || link != outside {
					close(stop)
					<-done
					t.Fatalf("moved link: %q %v", link, e)
				}
			} else {
				content, e := os.ReadFile(dest)
				if e != nil || !info.Mode().IsRegular() || string(content) != "inside" {
					close(stop)
					<-done
					t.Fatalf("partial move: %q %v", content, e)
				}
			}
			if e = os.Remove(dest); e != nil {
				close(stop)
				<-done
				t.Fatal(e)
			}
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "safe" {
		t.Fatalf("outside changed: %q %v", got, err)
	}
}

package fileop

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestExtractRejectsUnsafeEntries(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
	}{
		{"../x", 0}, {"/etc/cron.d/x", 0}, {`a\b`, 0}, {"bad\x00name", 0}, {"link", os.ModeSymlink | 0777}, {"device", os.ModeDevice | 0600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			root, err := os.OpenRoot(d)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			var b bytes.Buffer
			w := zip.NewWriter(&b)
			good, _ := w.Create("good")
			good.Write([]byte("ok"))
			h := &zip.FileHeader{Name: tc.name}
			h.SetMode(tc.mode)
			if _, err = w.CreateHeader(h); err != nil {
				t.Fatal(err)
			}
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(d, "input.zip"), b.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if err = extract(root, ArchiveRequest{Archive: "input.zip", Dest: "out"}); err != unix.EINVAL {
				t.Fatalf("got %v", err)
			}
			if _, err = os.Stat(filepath.Join(d, "out")); !os.IsNotExist(err) {
				t.Fatalf("destination exists: %v", err)
			}
			entries, err := os.ReadDir(d)
			if err != nil || len(entries) != 1 {
				t.Fatalf("staging not removed: %v %v", entries, err)
			}
		})
	}
}

func TestExtractCaps(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		size  uint32
	}{
		{"entry", 1, archiveFileBytes + 1}, {"total", 9, archiveFileBytes}, {"count", archiveEntries + 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			w := zip.NewWriter(&b)
			for i := 0; i < tc.count; i++ {
				if _, err := w.Create(fmt.Sprintf("f%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			data := b.Bytes()
			for off := 0; off+46 <= len(data); off++ {
				if binary.LittleEndian.Uint32(data[off:]) == 0x02014b50 {
					binary.LittleEndian.PutUint32(data[off+24:], tc.size)
					off += 45
				}
			}
			d := t.TempDir()
			root, err := os.OpenRoot(d)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err = os.WriteFile(filepath.Join(d, "in.zip"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = extract(root, ArchiveRequest{Archive: "in.zip", Dest: "out"}); err != unix.EINVAL {
				t.Fatalf("got %v", err)
			}
			if _, err = os.Stat(filepath.Join(d, "out")); !os.IsNotExist(err) {
				t.Fatalf("destination exists: %v", err)
			}
		})
	}
}

func TestExtractRejectsOversizedArchiveBeforeParsing(t *testing.T) {
	d := t.TempDir()
	f, err := os.Create(filepath.Join(d, "large.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(archiveTotalBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = extract(root, ArchiveRequest{Archive: "large.zip", Dest: "out"}); err != unix.EINVAL {
		t.Fatalf("got %v", err)
	}
	if _, err = os.Stat(filepath.Join(d, "out")); !os.IsNotExist(err) {
		t.Fatalf("destination exists: %v", err)
	}
}

func TestExtractZipBomb(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "bomb", Method: zip.Deflate}
	f, err := w.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := 0; i < 33; i++ {
		if _, err = f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if b.Len() > 1<<20 {
		t.Fatal("test archive did not compress")
	}
	d := t.TempDir()
	root, err := os.OpenRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = os.WriteFile(filepath.Join(d, "in.zip"), b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err = extract(root, ArchiveRequest{Archive: "in.zip", Dest: "out"}); err != unix.EINVAL {
		t.Fatalf("got %v", err)
	}
	if _, err = os.Stat(filepath.Join(d, "out")); !os.IsNotExist(err) {
		t.Fatalf("destination exists: %v", err)
	}
}

func TestArchiveSkipsSymlinksAndOwnTemp(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "file"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(d)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = archive(root, ArchiveRequest{Paths: []string{"."}, Dest: "out.zip"}); err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(filepath.Join(d, "out.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != 1 || z.File[0].Name != "file" {
		t.Fatalf("unexpected entries: %v", len(z.File))
	}
	if err = archive(root, ArchiveRequest{Paths: []string{"file"}, Dest: "out.zip"}); err != unix.EEXIST {
		t.Fatalf("overwrite: %v", err)
	}
}

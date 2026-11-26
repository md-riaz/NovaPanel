package fileop

import (
	"archive/zip"
	"bytes"
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

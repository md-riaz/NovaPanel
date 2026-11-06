package update

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func tarBytes(t *testing.T, headers []*tar.Header) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, h := range headers {
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := w.Write(bytes.Repeat([]byte("x"), int(h.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func binaryHeaders() []*tar.Header {
	return []*tar.Header{{Name: "panel-agent", Mode: 0755, Size: 1}, {Name: "panel-web", Mode: 0755, Size: 1}, {Name: "novapanelctl", Mode: 0755, Size: 1}}
}
func TestDefensiveExtraction(t *testing.T) {
	b := tarBytes(t, binaryHeaders())
	sum := sha256.Sum256(b)
	a := Artifact{Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
	root := t.TempDir()
	dir, err := stage(context.Background(), root, a, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0700 {
		t.Fatalf("staging must remain private: stat=%v err=%v", st, err)
	}
	for _, name := range []string{"panel-agent", "panel-web", "novapanelctl"} {
		s, e := os.Lstat(filepath.Join(dir, name))
		if e != nil || s.Mode() != 0755 {
			t.Fatal(s, e)
		}
	}
	for _, h := range []*tar.Header{{Name: "../escape", Size: 1}, {Name: "/escape", Size: 1}, {Name: "dir/panel-agent", Size: 1}, {Name: "panel-agent", Typeflag: tar.TypeSymlink, Linkname: "outside"}, {Name: "panel-agent", Typeflag: tar.TypeLink, Linkname: "panel-web"}, {Name: "panel-agent", Typeflag: tar.TypeDir}, {Name: "panel-agent", Typeflag: tar.TypeFifo}, {Name: "extra", Size: 1}, {Name: "panel-agent", Size: 0}} {
		b = tarBytes(t, []*tar.Header{h})
		sum = sha256.Sum256(b)
		a.Size = int64(len(b))
		a.SHA256 = hex.EncodeToString(sum[:])
		before, _ := os.ReadDir(root)
		_, err = stage(context.Background(), root, a, bytes.NewReader(b))
		code(t, err, "invalid_argument")
		after, _ := os.ReadDir(root)
		if len(before) != len(after) {
			t.Fatal("failed stage leaked files")
		}
	}
	for _, headers := range [][]*tar.Header{binaryHeaders()[:2], append(binaryHeaders(), binaryHeaders()[0])} {
		b = tarBytes(t, headers)
		sum = sha256.Sum256(b)
		a.Size = int64(len(b))
		a.SHA256 = hex.EncodeToString(sum[:])
		_, err = stage(context.Background(), root, a, bytes.NewReader(b))
		code(t, err, "invalid_argument")
	}
}

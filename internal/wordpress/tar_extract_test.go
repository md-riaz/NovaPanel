package wordpress

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"strings"
	"testing"
)

func TestExtractTarRejectsSymlinkParent(t *testing.T) {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "wordpress/link/child", Mode: 0644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(t.TempDir(), dir+"/link"); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := extractTar(context.Background(), bytes.NewReader(b.Bytes()), r); err == nil {
		t.Fatal("symlink parent accepted")
	}
}

func TestExtractOfficialTarFixture(t *testing.T) {
	file := os.Getenv("NOVAPANEL_PINNED_TARBALL")
	if file == "" {
		t.Skip("pinned tarball fixture unavailable")
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := extractTar(context.Background(), f, r); err != nil {
		t.Fatal(err)
	}
	name := "wp-includes/php-ai-client/src/Providers/ApiBasedImplementation/AbstractApiBasedModelMetadataDirectory.php"
	if _, err := r.Stat(name); err != nil {
		t.Fatalf("long name missing: %v", err)
	}
}

func TestExtractTarReplayAndLongPAXPath(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	name := "wordpress/wp-includes/" + strings.Repeat("component/", 15) + "result.php"
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: 4, Format: tar.FormatPAX}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("core")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := 0; i < 2; i++ {
		if err := extractTar(context.Background(), bytes.NewReader(compressed.Bytes()), r); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	b, err := r.ReadFile(strings.TrimPrefix(name, "wordpress/"))
	if err != nil || string(b) != "core" {
		t.Fatalf("long path: %q %v", b, err)
	}
}

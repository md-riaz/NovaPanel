package wordpress

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyDownloadedArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "archive")
	data := []byte("fixture archive")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	expected := hex.EncodeToString(digest[:])
	if err := verifyDownloadedArchive(path, uint32(os.Getuid()), expected); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDownloadedArchive(path, uint32(os.Getuid())); err == nil {
		t.Fatal("accepted mismatched archive")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := verifyDownloadedArchive(link, uint32(os.Getuid()), expected); err == nil {
		t.Fatal("accepted symlink")
	}
}

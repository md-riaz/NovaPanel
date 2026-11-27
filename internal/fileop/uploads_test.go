package fileop

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUploadPlacement(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "site", "public")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	req := Request{Base: base, Name: "site", Path: "file", Length: 3}
	if err := upload(req, bytes.NewBufferString("abc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := upload(req, bytes.NewBufferString("xyz")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(data) != "xyz" {
		t.Fatalf("content %q: %v", data, err)
	}
	info, err := os.Stat(filepath.Join(dir, "file"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v %v", info, err)
	}
	for _, tc := range []struct {
		path    string
		length  int
		payload string
	}{{"file", 2, "abc"}, {"file", 4, "abc"}, {"../bad", 3, "abc"}, {`bad\name`, 3, "abc"}, {"file", MaxUpload + 1, ""}} {
		req.Path, req.Length = tc.path, tc.length
		if err := upload(req, bytes.NewBufferString(tc.payload)); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	data, err = os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(data) != "xyz" {
		t.Fatalf("failed upload changed file: %q %v", data, err)
	}
}

func TestUploadAtMaximum(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "site", "public"), 0755); err != nil {
		t.Fatal(err)
	}
	req := Request{Base: base, Name: "site", Path: "large", Length: MaxUpload}
	if err := upload(req, io.LimitReader(zeroReader{}, MaxUpload)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(base, "site", "public", "large"))
	if err != nil || info.Size() != MaxUpload {
		t.Fatalf("large upload: %v %v", info, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestSweepUploads(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for _, name := range []string{"old", "fresh"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(dir, "old")
	if err := os.Chtimes(old, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	SweepUploads(dir, now)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fresh")); err != nil {
		t.Fatal(err)
	}
}

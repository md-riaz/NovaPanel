package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestStageGzipAndArchiveBoundaries(t *testing.T) {
	valid := tarBytes(t, binaryHeaders())
	gzipBytes := func(body []byte) []byte {
		var b bytes.Buffer
		w := gzip.NewWriter(&b)
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	corrupt := gzipBytes(valid)
	corrupt[len(corrupt)-8] ^= 1
	pax := binaryHeaders()
	pax[0].Format = tar.FormatPAX
	pax[0].PAXRecords = map[string]string{"comment": "reject metadata"}
	for _, tc := range []struct {
		name  string
		body  []byte
		valid bool
	}{
		{"gzip", gzipBytes(valid), true},
		{"checksum", corrupt, false},
		{"pax", tarBytes(t, pax), false},
		{"trailing payload", append(append([]byte{}, valid...), 'x'), false},
		{"truncated gzip", gzipBytes(valid)[:20], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sum := sha256.Sum256(tc.body)
			a := Artifact{Size: int64(len(tc.body)), SHA256: hex.EncodeToString(sum[:])}
			dir, err := stage(context.Background(), root, a, bytes.NewReader(tc.body))
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			} else {
				code(t, err, "invalid_argument")
				if dir != "" {
					t.Fatal("invalid staging directory returned", dir)
				}
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("staging leaked: %v %v", entries, err)
			}
		})
	}
}

func TestRecoveryRefusesUnexpectedCurrent(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"1.0.0", "1.2.3", "2.0.0"} {
		if err := os.Mkdir(filepath.Join(root, v), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("2.0.0", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"previous":"1.0.0","next":"1.2.3"}`)
	path := filepath.Join(root, ".update-pending")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{}
	code(t, recoverApply(context.Background(), root, r), "conflict")
	if target, err := os.Readlink(filepath.Join(root, "current")); err != nil || target != "2.0.0" || r.calls != 0 {
		t.Fatal(target, err, r.calls)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, body) {
		t.Fatal("recovery evidence changed", err)
	}
}

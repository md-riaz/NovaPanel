package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/fileop"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
)

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("disconnected") }

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestUpload(t *testing.T) {
	for _, tc := range []struct {
		name, role, csrf, origin string
		body                     io.Reader
		cookie                   bool
		status                   int
	}{
		{"success", "admin", "expected", "same-origin", strings.NewReader("hello"), true, 201},
		{"unauthenticated", "admin", "expected", "same-origin", strings.NewReader("x"), false, 401},
		{"readonly", "read_only", "expected", "same-origin", strings.NewReader("x"), true, 403},
		{"csrf", "admin", "wrong", "same-origin", strings.NewReader("x"), true, 403},
		{"missing csrf", "admin", "", "same-origin", strings.NewReader("x"), true, 403},
		{"origin", "admin", "expected", "cross-site", strings.NewReader("x"), true, 403},
		{"disconnect", "admin", "expected", "same-origin", brokenReader{}, true, 500},
		{"oversize", "admin", "expected", "same-origin", io.LimitReader(zeros{}, fileop.MaxUpload+1), true, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			f := &fakeRPC{reply: func(v string) (rpc.Reply, error) {
				return rpc.Reply{OK: true, Data: map[string]any{"role": tc.role, "csrf_token": "expected"}}, nil
			}}
			h, err := New(Options{Origin: "https://panel.example", UploadsDir: dir, Dial: func() (RPCClient, error) { return f, nil }})
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "https://panel.example/uploads", tc.body)
			r.Header.Set("Content-Type", "application/octet-stream")
			r.Header.Set("Sec-Fetch-Site", tc.origin)
			r.Header.Set("X-CSRF-Token", tc.csrf)
			if tc.cookie {
				r.AddCookie(&http.Cookie{Name: CookieName, Value: "nps_session"})
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.status != 201 {
				if len(entries) != 0 {
					t.Fatalf("unexpected files: %v", entries)
				}
				return
			}
			var result struct {
				UploadID string `json:"upload_id"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil || !id.Valid(result.UploadID) {
				t.Fatalf("invalid response: %s", w.Body.String())
			}
			if len(entries) != 1 || entries[0].Name() != result.UploadID {
				t.Fatalf("files: %v", entries)
			}
			path := filepath.Join(dir, result.UploadID)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != 5 {
				t.Fatalf("invalid file: %v", info)
			}
			b, err := os.ReadFile(path)
			if err != nil || string(b) != "hello" {
				t.Fatalf("content %q: %v", b, err)
			}
		})
	}
}

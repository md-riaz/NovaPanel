package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"novapanel/internal/id"
	"novapanel/internal/store"
)

func TestSweepLiveUploadStates(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := New()
	if a.Cfg.UploadsDir != "/var/spool/novapanel/uploads" {
		t.Fatalf("upload spool default: %s", a.Cfg.UploadsDir)
	}
	a.Store = s
	a.Cfg.UploadsDir = t.TempDir()
	now := time.Now()
	for _, tc := range []struct {
		state, code string
		keep        bool
	}{{"queued", "", true}, {"running", "", true}, {"failed", "interrupted", true}, {"done", "", false}, {"failed", "invalid_argument", false}} {
		uploadID, _ := id.New()
		jobID, _ := id.New()
		args, _ := json.Marshal(map[string]string{"upload_id": uploadID})
		if err := s.CreateJob(ctx, store.Job{ID: jobID, Verb: "files.upload", Args: args, State: tc.state, Code: tc.code}); err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(a.Cfg.UploadsDir, uploadID)
		if err := os.WriteFile(name, nil, 0600); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-2 * time.Hour)
		if err := os.Chtimes(name, old, old); err != nil {
			t.Fatal(err)
		}
		if err := a.SweepUploads(ctx, now); err != nil {
			t.Fatal(err)
		}
		_, err := os.Stat(name)
		if tc.keep && err != nil || !tc.keep && !os.IsNotExist(err) {
			t.Fatalf("state %s code %s: %v", tc.state, tc.code, err)
		}
	}
}

func TestSweepBadJobAndQueryFailure(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := New()
	a.Store = s
	a.Cfg.UploadsDir = t.TempDir()
	jobID, _ := id.New()
	if err := s.CreateJob(ctx, store.Job{ID: jobID, Verb: "files.upload", Args: json.RawMessage(`{}`), State: "queued"}); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(a.Cfg.UploadsDir, "orphan")
	if err := os.WriteFile(name, nil, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.SweepUploads(canceled, now); err == nil {
		t.Fatal("query failure ignored")
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("query failure removed file: %v", err)
	}
	if err := a.SweepUploads(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("malformed row blocked cleanup: %v", err)
	}
}

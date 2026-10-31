package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/secret"
	"novapanel/internal/store"
)

func TestSealedInputSurvivesRestartAndTerminalCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	seal, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	q := NewQueue(s, seal, &fakeAudit{})
	ctx := WithMetadata(context.Background(), Metadata{Actor: "owner", Role: "owner", CreatedBy: "token", Scopes: []string{"sites:write"}, Automation: true})
	plain := map[string]string{"site_id": "site1", "cert_pem": "test-certificate-marker", "key_pem": "test-private-key-marker"}
	r, err := q.EnqueueSealed(ctx, "ssl.upload", plain, "site:site1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Job.Effectful || r.ClaimToken == "" {
		t.Fatal("changed job class or lost claim")
	}
	if _, err = s.StartJob(ctx, r.Job.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	if err = s.RecoverJobs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "queued" || !bytes.Equal(j.Args, r.Job.Args) || j.CreatedBy != "token" || !j.Automation || len(j.Scopes) != 1 {
		t.Fatal("recovery changed input or authority")
	}
	raw, err := q.OpenInput(&j)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var got map[string]string
	if err = json.Unmarshal(raw, &got); err != nil || got["key_pem"] != plain["key_pem"] {
		t.Fatal("input round trip failed")
	}
	// No plaintext may reach SQLite, including WAL and free pages.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{plain["cert_pem"], plain["key_pem"]} {
			if bytes.Contains(data, []byte(marker)) {
				t.Fatalf("plaintext leaked into %s", entry.Name())
			}
		}
	}
	j.State = "done"
	if err = s.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, j.ID)
	if err != nil || string(j.Args) != "{}" {
		t.Fatal("terminal input retained", err)
	}
}

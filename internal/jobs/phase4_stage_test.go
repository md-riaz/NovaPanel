package jobs

import (
	"context"
	"path/filepath"
	"testing"

	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

func TestWordPressStageRequiresOriginalWorker(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seal, _ := secret.New(make([]byte, 32))
	q := NewQueue(s, seal, &fakeAudit{})
	if err := q.StageWordPress(context.Background(), "original-job", "/missing", wordpress.StagedPaths{}, nil); err == nil {
		t.Fatal("unlocked stage admitted")
	}
}

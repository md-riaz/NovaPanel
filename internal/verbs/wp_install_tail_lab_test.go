//go:build wordpress_offline_lab && linux

package verbs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

func TestWordPressLabNonemptyDocrootRequiresReconciliation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo", "public")
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "keep")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	a := agent.New()
	a.Cfg.SitesRoot = root
	op := store.Operation{Checkpoint: "db_applied", Identity: store.OperationIdentity{WordPress: &store.WordPressReservation{Input: wordpress.InstallInput{SiteName: "demo"}}}}
	err := wpInstallTail(context.Background(), a.Store, a.Exec, a.Cfg.SitesRoot, &jobs.Job{}, op, jobs.WordPressCredentials{})
	coded, ok := err.(*rpcerr.CodedError)
	if !ok || coded.Code != "precondition_failed" || !strings.Contains(coded.Msg, "reconciliation") {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "original" {
		t.Fatalf("docroot changed: %q %v", b, err)
	}
}

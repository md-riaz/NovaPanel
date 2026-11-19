//go:build wordpress_offline_lab && linux

package verbs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"novapanel/internal/wordpress"
)

type labAccountRunner struct{ root, id string }

func (r labAccountRunner) Run(_ context.Context, _ string, _ []string, _ runner.RunOpts) ([]byte, []byte, int, error) {
	return []byte(fmt.Sprintf("demo:x:1001:1001:novapanel-site=%s:%s:/usr/sbin/nologin\n", r.id, filepath.Join(r.root, "demo"))), nil, 0, nil
}

func TestWordPressLabBackendRefusalIsAgentError(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires disposable root container")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "demo", "public")
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	in := wordpress.InstallInput{SiteName: "demo", UID: 1001, GID: 1001, DatabaseID: sid, DatabaseUserID: sid}
	op := store.Operation{SiteID: sid, Checkpoint: "db_applied", Identity: store.OperationIdentity{WordPress: &store.WordPressReservation{Input: in}}}
	creds := jobs.WordPressCredentials{DatabasePassword: "password"}
	for i := range creds.Salts {
		creds.Salts[i] = "salt"
	}
	err := wpInstallTail(context.Background(), nil, labAccountRunner{root: root, id: sid}, root, &jobs.Job{}, op, creds)
	coded, ok := err.(*rpcerr.CodedError)
	if !ok || coded.Code != "agent_error" {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wp-config.php")); !os.IsNotExist(err) {
		t.Fatalf("config published: %v", err)
	}
}

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

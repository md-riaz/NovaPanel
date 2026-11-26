package verbs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

const deployTestSite = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func TestDeployRegistryAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		role rpc.Role
		ro   bool
	}{{"deploy.configure", rpc.Admin, false}, {"deploy.status", rpc.ReadOnly, true}, {"deploy.history", rpc.ReadOnly, true}} {
		v, ok := rpc.Registry[tc.name]
		if !ok || v.MinRole != tc.role || v.Scope != "deploy" || v.ReadOnly != tc.ro || v.MFA || v.Slow || v.Effectful || v.Exec != nil || v.NewJobArgs != nil {
			t.Fatalf("invalid registration %s: %+v", tc.name, v)
		}
	}
	for _, raw := range []string{"file:///tmp/repo", "ext::evil", "ftp://example.com/repo"} {
		if (&deployConfigureArgs{deployTestSite, raw, "main"}).Validate() == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"https://example.com/repo", "ssh://git@example.com/repo"} {
		if err := (&deployConfigureArgs{deployTestSite, raw, "main"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, branch := range []string{"", "-bad", "a..b", "a/.hidden", "a.lock", "a b"} {
		if validBranch(branch) {
			t.Fatalf("accepted branch %q", branch)
		}
	}
}

func TestDeployConfigureAndReads(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateSite(ctx, store.Site{ID: deployTestSite, Name: "demo", Type: "static", Docroot: "/srv/sites/demo/public"}, store.Domain{ID: "domain", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSiteStatus(ctx, deployTestSite, "active"); err != nil {
		t.Fatal(err)
	}
	seal, _ := secret.New(make([]byte, 32))
	a := agent.New()
	a.Store = s
	a.Secret = seal
	a.Jobs = jobs.NewQueue(s, seal, nil)
	if _, err := deployStatus(ctx, a, &siteIDArgs{deployTestSite}); err == nil {
		t.Fatal("unconfigured status succeeded")
	}
	args := &deployConfigureArgs{deployTestSite, "https://example.com/repo", "main"}
	result, err := deployConfigure(ctx, a, args)
	if err != nil {
		t.Fatal(err)
	}
	_ = result
	cfg, err := s.DeployConfig(ctx, deployTestSite)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cfg.PublicKey))
	if err != nil || parsed.Type() != ssh.KeyAlgoED25519 {
		t.Fatalf("public key: %v", err)
	}
	sealed, err := s.DeployKey(ctx, deployTestSite)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := seal.Open(secret.Sealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plain)
	if len(plain) != ed25519.SeedSize {
		t.Fatal("invalid private key")
	}
	private := ed25519.NewKeyFromSeed(plain)
	defer clear(private)
	pub, err := ssh.NewPublicKey(private.Public())
	if err != nil || !bytes.Equal(pub.Marshal(), parsed.Marshal()) {
		t.Fatal("key mismatch")
	}
	args.RepoURL = "ssh://git@example.com/repo"
	args.Branch = "next"
	if _, err = deployConfigure(ctx, a, args); err != nil {
		t.Fatal(err)
	}
	next, _ := s.DeployConfig(ctx, deployTestSite)
	again, _ := s.DeployKey(ctx, deployTestSite)
	if next.PublicKey != cfg.PublicKey || next.RepoURL != args.RepoURL || next.Branch != "next" || !bytes.Equal(again.Ciphertext, sealed.Ciphertext) {
		t.Fatal("configure rotated key or lost update")
	}
	status, err := deployStatus(ctx, a, &siteIDArgs{deployTestSite})
	if err != nil || status == nil {
		t.Fatal(err)
	}
	var statusData struct {
		SHA       string `json:"current_sha"`
		State     string `json:"state"`
		LastRun   int64  `json:"last_run"`
		PublicKey string `json:"deploy_pubkey"`
	}
	b, _ := json.Marshal(status)
	if err = json.Unmarshal(b, &statusData); err != nil || statusData.SHA != "" || statusData.State != "not_deployed" || statusData.PublicKey != cfg.PublicKey {
		t.Fatalf("empty status: %s %v", b, err)
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		for i := 0; i < 105; i++ {
			state := "failed"
			if i == 50 || i == 90 {
				state = "success"
			}
			if _, e := tx.ExecContext(ctx, "INSERT INTO deploys(id,site_id,repo_url,branch,sha,state,created) VALUES(?,?,?,?,?,?,?)", fmt.Sprintf("%03d", i), deployTestSite, args.RepoURL, args.Branch, fmt.Sprintf("sha%d", i), state, i); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.DeployHistory(ctx, deployTestSite, 100, false)
	if err != nil || len(rows) != 100 || rows[0].SHA != "sha104" {
		t.Fatalf("history: %v %v", rows, err)
	}
	rows, err = s.DeployHistory(ctx, deployTestSite, 1, true)
	if err != nil || len(rows) != 1 || rows[0].SHA != "sha90" {
		t.Fatalf("success: %v %v", rows, err)
	}
	status, err = deployStatus(ctx, a, &siteIDArgs{deployTestSite})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(status)
	if err = json.Unmarshal(b, &statusData); err != nil || statusData.SHA != "sha90" || statusData.State != "success" || statusData.LastRun != 90 {
		t.Fatalf("status: %s %v", b, err)
	}
	for _, tc := range []struct{ limit, want int }{{0, 20}, {3, 3}, {1000, 100}} {
		got, e := deployHistory(ctx, a, &deployHistoryArgs{SiteID: deployTestSite, Limit: tc.limit})
		if e != nil {
			t.Fatal(e)
		}
		b, _ = json.Marshal(got)
		var result struct {
			Deploys []store.Deploy `json:"deploys"`
		}
		if e = json.Unmarshal(b, &result); e != nil || len(result.Deploys) != tc.want || result.Deploys[0].SHA != "sha104" {
			t.Fatalf("history: %s %v", b, e)
		}
	}
	if err = s.DeleteDeployKey(ctx, deployTestSite); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDeployKey(ctx, deployTestSite); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeployKey(ctx, deployTestSite); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err = s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "DELETE FROM sites WHERE id=?", deployTestSite)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DeployConfig(ctx, deployTestSite); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	rows, err = s.DeployHistory(ctx, deployTestSite, 100, false)
	if err != nil || len(rows) != 0 {
		t.Fatal("history did not cascade")
	}
}

func TestDeployFileCleanup(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "deploy", deployTestSite)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"identity", "known_hosts"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := removeDeployFiles(filepath.Join(root, "panel.db"), deployTestSite); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"identity", "known_hosts"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("deploy file remains: %v", err)
		}
	}
}

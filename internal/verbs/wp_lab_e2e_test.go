//go:build wordpress_offline_lab && linux

package verbs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/audit"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type wpLabRunner struct {
	account  *accountRunner
	db       *databaseRunner
	installs int
}

func (r *wpLabRunner) Run(ctx context.Context, prog string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" {
		return r.account.Run(ctx, prog, args, opts)
	}
	if prog == "/usr/bin/php8.3" {
		r.installs++
		return nil, nil, 0, nil
	}
	return r.db.Run(ctx, prog, args, opts)
}

func TestWordPressLabInstall(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NOVAPANEL_DISPOSABLE_UID_LAB") != "1" {
		t.Skip("requires root and NOVAPANEL_DISPOSABLE_UID_LAB=1")
	}
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log, err := audit.Open(st, filepath.Join(root, "audit"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	a := agent.New()
	a.Store = st
	a.Audit = log
	a.Cfg.SitesRoot = filepath.Join(root, "sites")
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	const sid = "01K8WMXZF57CGNZKX1FZ38DZMA"
	docroot := filepath.Join(a.Cfg.SitesRoot, "demo", "public")
	if err = os.MkdirAll(docroot, 0750); err != nil {
		t.Fatal(err)
	}
	if err = os.Chown(docroot, 1001, 1001); err != nil {
		t.Fatal(err)
	}
	version := "8.3"
	_, err = st.CreateSite(ctx, store.Site{ID: sid, Name: "demo", Type: "wordpress", Docroot: docroot, PHPVersion: &version}, store.Domain{ID: "01K8WMXZF57CGNZKX1FZ38DZMB", FQDN: "demo.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.SetSiteStatus(ctx, sid, "active"); err != nil {
		t.Fatal(err)
	}
	fake := &wpLabRunner{account: &accountRunner{root: a.Cfg.SitesRoot, name: "demo", id: sid, marker: "novapanel-site="}, db: &databaseRunner{}}
	a.Exec = fake
	server := rpc.NewServer(a)
	server.ConfigureJobs()
	if err = a.Jobs.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	reply := server.Dispatch(rpc.WithCaller(ctx, rpc.RootCaller(0)), rpc.Request{Verb: "wp.install", Args: json.RawMessage(fmt.Sprintf(`{"site_id":%q,"title":"Example","admin_username":"admin","admin_email":"admin@example.test"}`, sid))})
	if !reply.OK {
		t.Fatalf("admission: %+v", reply)
	}
	en := reply.Data.(jobs.EnqueueResult)
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Jobs.Run(run) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	var j store.Job
	for {
		j, err = st.GetJob(ctx, en.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == "done" || j.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			op, e := st.LoadOperation(ctx, en.Job.ID)
			_, ce := a.Jobs.OpenWordPressCredentials(ctx, en.Job.ID)
			t.Fatalf("timeout: state=%s attempts=%d checkpoint=%s opErr=%v credentialErr=%v dbCalls=%d", j.State, j.Attempts, op.Checkpoint, e, ce, fake.db.calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if j.State != "done" || string(j.Result) != fmt.Sprintf(`{"site_id":%q}`, sid) || fake.installs != 1 {
		t.Fatalf("install: %+v calls=%d", j, fake.installs)
	}
	if _, err = st.WordPressDatabaseID(ctx, sid); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(docroot, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(docroot, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != 0400 || stat.Uid != 1001 || stat.Gid != 1001 {
		t.Fatalf("config mode/owner: %v %d:%d", info.Mode(), stat.Uid, stat.Gid)
	}
	for _, name := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		if !strings.Contains(string(config), name) {
			t.Errorf("missing %s", name)
		}
	}
	claim, err := a.Jobs.Claim(jobs.WithMetadata(ctx, jobs.Metadata{CreatedBy: "local-root"}), en.Job.ID, en.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	db := claim["database"].(map[string]any)
	admin := claim["wordpress"].(map[string]any)
	if !strings.Contains(string(config), db["password"].(string)) || admin["admin_password"] == "" {
		t.Fatal("missing claim credentials")
	}
}

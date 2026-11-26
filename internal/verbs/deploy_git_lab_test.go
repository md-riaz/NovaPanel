//go:build deploy_lab && linux

package verbs

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"novapanel/internal/agent"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type labRunner struct {
	helper string
	t      *testing.T
}

func (r labRunner) Run(ctx context.Context, prog string, args []string, o runner.RunOpts) ([]byte, []byte, int, error) {
	if len(args) == 1 && args[0] == "__deploy" {
		prog = r.helper
	}
	out, stderr, code, err := (runner.Real{}).Run(ctx, prog, args, o)
	if code != 0 && prog == "/usr/bin/git" {
		r.t.Logf("git %q: %s", args, stderr)
	}
	return out, stderr, code, err
}
func labCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	paths := map[string]string{"git": "/usr/bin/git", "go": "/usr/local/go/bin/go", "useradd": "/usr/sbin/useradd", "mkdir": "/usr/bin/mkdir", "chown": "/usr/bin/chown", "id": "/usr/bin/id"}
	prog := paths[args[0]]
	out, stderr, code, err := (runner.Real{}).Run(context.Background(), prog, args[1:], runner.RunOpts{Dir: dir, Env: os.Environ()})
	if err != nil || code != 0 {
		t.Fatalf("command %q: %v: %s", args, err, stderr)
	}
	return strings.TrimSpace(string(out))
}
func TestDeployGitLab(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires isolated root container")
	}
	ctx := context.Background()
	root := t.TempDir()
	if e := os.Chmod(filepath.Dir(root), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(root, 0755); e != nil {
		t.Fatal(e)
	}
	name := "npdeploy151"
	home := filepath.Join(root, name)
	work := filepath.Join(root, "source")
	bare := filepath.Join(root, "origin.git")
	if e := os.Mkdir(work, 0755); e != nil {
		t.Fatal(e)
	}
	labCommand(t, work, "git", "init", "-b", "main")
	labCommand(t, work, "git", "config", "user.name", "Test")
	labCommand(t, work, "git", "config", "user.email", "test@example.com")
	write := func(s string) {
		if e := os.WriteFile(filepath.Join(work, "index.html"), []byte(s), 0644); e != nil {
			t.Fatal(e)
		}
		labCommand(t, work, "git", "add", "index.html")
		labCommand(t, work, "git", "commit", "-m", s)
	}
	write("first")
	first := labCommand(t, work, "git", "rev-parse", "HEAD")
	labCommand(t, root, "git", "clone", "--bare", work, bare)
	labCommand(t, root, "useradd", "-U", "-M", "-d", home, "-s", "/usr/sbin/nologin", "-c", "novapanel-site="+deployTestSite, name)
	t.Cleanup(func() {
		(runner.Real{}).Run(context.Background(), "/usr/sbin/userdel", []string{name}, runner.RunOpts{Env: os.Environ()})
	})
	uid := labCommand(t, root, "id", "-u", name)
	gid := labCommand(t, root, "id", "-g", name)
	labCommand(t, root, "mkdir", "-p", filepath.Join(home, "public"))
	labCommand(t, root, "chown", "-R", uid+":"+gid, home)
	labCommand(t, root, "chown", "-R", uid+":"+gid, bare)
	if e := os.Chmod(home, 0751); e != nil {
		t.Fatal(e)
	}
	helper := filepath.Join(root, "panel-agent")
	labCommand(t, "../..", "go", "build", "-o", helper, "./cmd/panel-agent")
	s, e := store.Open(filepath.Join(root, "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	_, e = s.CreateSite(ctx, store.Site{ID: deployTestSite, Name: name, Type: "static", Docroot: filepath.Join(home, "public")}, store.Domain{ID: "domain", FQDN: "demo.test"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetSiteStatus(ctx, deployTestSite, "active"); e != nil {
		t.Fatal(e)
	}
	seal, _ := secret.New(make([]byte, 32))
	sealed, _ := seal.Seal(make([]byte, 32))
	if e = s.CreateDeployConfig(ctx, store.DeployConfig{SiteID: deployTestSite, RepoURL: bare, Branch: "main", PublicKey: "lab"}, store.ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}); e != nil {
		t.Fatal(e)
	}
	a := agent.New()
	a.Cfg.SitesRoot = root
	a.Store = s
	a.Exec = labRunner{helper, t}
	a.Secret = seal
	run := func(id, ref, sha string) {
		t.Helper()
		j := &jobs.Job{ID: id, ObjectKey: "site:" + deployTestSite}
		if e := (deployService{a}).execute(ctx, j, deployTestSite, ref, sha); e != nil {
			t.Fatal(e)
		}
	}
	run("first", "main", "")
	if got, e := os.ReadFile(filepath.Join(home, "public/index.html")); e != nil || string(got) != "first" {
		t.Fatalf("first checkout %s %v", got, e)
	}
	var info unix.Stat_t
	e = unix.Stat(filepath.Join(home, "public/index.html"), &info)
	if e != nil {
		t.Fatal(e)
	}
	owner := info.Uid
	wantUID, _ := strconv.ParseUint(uid, 10, 32)
	if owner != uint32(wantUID) {
		t.Fatalf("checkout owner %d, want %d", owner, wantUID)
	}
	if _, e = os.Stat(filepath.Join(home, "public/.git")); !os.IsNotExist(e) {
		t.Fatal("git state in docroot")
	}
	if _, e = os.Stat(filepath.Join(home, ".novapanel/deploy/repo.git")); e != nil {
		t.Fatal(e)
	}
	write("second")
	labCommand(t, root, "chown", "-R", "0:0", bare)
	labCommand(t, work, "git", "push", bare, "main")
	labCommand(t, root, "chown", "-R", uid+":"+gid, bare)
	run("second", "main", "")
	if got, _ := os.ReadFile(filepath.Join(home, "public/index.html")); string(got) != "second" {
		t.Fatal(string(got))
	}
	run("rollback", "", first)
	if got, _ := os.ReadFile(filepath.Join(home, "public/index.html")); string(got) != "first" {
		t.Fatal(string(got))
	}
	gidNum, _ := strconv.ParseUint(gid, 10, 32)
	dropped := deployUserRunner{runner: a.Exec, uid: uint32(wantUID), gid: uint32(gidNum)}
	if e = deployHelper(ctx, dropped, home, "prepare", []byte("lab key")); e != nil {
		t.Fatal(e)
	}
	identity := filepath.Join(home, ".novapanel/deploy/id_ed25519")
	var keyStat unix.Stat_t
	if e = unix.Stat(identity, &keyStat); e != nil || keyStat.Uid != uint32(wantUID) || keyStat.Mode&0777 != 0600 {
		t.Fatalf("identity ownership or mode: %+v %v", keyStat, e)
	}
	if e = deployHelper(ctx, dropped, home, "remove", nil); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(home, ".novapanel/deploy/id_ed25519")); !os.IsNotExist(e) {
		t.Fatal("identity not removed")
	}
}

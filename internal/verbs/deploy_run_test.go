package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"novapanel/internal/agent"
	run "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

type deployFake struct {
	t             *testing.T
	home, url     string
	foreign       bool
	commands      [][]string
	operations    []string
	missingCommit bool
	failFetch     bool
}

func (f *deployFake) Run(_ context.Context, prog string, args []string, opts run.RunOpts) ([]byte, []byte, int, error) {
	if prog == "/usr/bin/getent" {
		if args[0] == "group" {
			return []byte("www-data:x:33:\n"), nil, 0, nil
		}
		if args[1] == "www-data" {
			return []byte("www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin\n"), nil, 0, nil
		}
		return []byte(fmt.Sprintf("demo:x:1234:1234:novapanel-site=%s:%s:/usr/sbin/nologin\n", deployTestSite, f.home)), nil, 0, nil
	}
	if len(args) == 1 && args[0] == "__deploy" {
		if opts.Uid != 1234 || opts.Gid != 1234 || len(opts.Groups) != 0 {
			f.t.Fatal("helper did not drop credentials")
		}
		b, e := io.ReadAll(opts.Stdin)
		if e != nil {
			f.t.Fatal(e)
		}
		var v struct {
			Operation string
			Key       []byte
		}
		if json.Unmarshal(b, &v) != nil {
			f.t.Fatal("bad helper request")
		}
		path := filepath.Join(f.home, ".novapanel", "deploy", "id_ed25519")
		if v.Operation == "remove" {
			if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
				f.t.Fatal(e)
			}
		}
		if v.Operation == "prepare" {
			if _, e := os.Stat(path); !os.IsNotExist(e) {
				f.t.Fatal("stale identity was not removed first")
			}
			if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				f.t.Fatal(e)
			}
			if e := os.WriteFile(path, v.Key, 0600); e != nil {
				f.t.Fatal(e)
			}
		}
		f.operations = append(f.operations, v.Operation)
		if v.Operation == "prepare" && (!strings.Contains(string(v.Key), "OPENSSH PRIVATE KEY") || strings.Contains(strings.Join(args, " "), "PRIVATE")) {
			f.t.Fatal("missing stdin key")
		}
		return nil, nil, 0, nil
	}
	if prog != "/usr/bin/git" {
		f.t.Fatalf("unexpected program %s", prog)
	}
	if opts.Uid != 1234 || opts.Gid != 1234 || len(opts.Groups) != 0 {
		f.t.Fatal("git credentials")
	}
	base := []string{"--git-dir=" + filepath.Join(f.home, ".novapanel/deploy/repo.git"), "--work-tree=" + filepath.Join(f.home, "public"), "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}
	if len(args) < len(base) || strings.Join(args[:len(base)], "|") != strings.Join(base, "|") {
		f.t.Fatalf("git flags %q", args)
	}
	cmd := append([]string(nil), args[len(base):]...)
	f.commands = append(f.commands, cmd)
	if cmd[0] == "fetch" && f.failFetch {
		return nil, nil, 1, errors.New("fetch failed")
	}
	if cmd[0] == "cat-file" && f.missingCommit {
		return nil, nil, 1, errors.New("missing object")
	}
	switch cmd[0] {
	case "remote":
		if cmd[1] == "get-url" {
			if f.foreign {
				return []byte("ssh://evil/repo\n"), nil, 0, nil
			}
			return []byte(f.url + "\n"), nil, 0, nil
		}
	case "ls-remote":
		return []byte(testSHA + "\trefs/heads/main\n"), nil, 0, nil
	}
	return nil, nil, 0, nil
}
func deployFixture(t *testing.T, configured bool) (*agent.Agent, *deployFake) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "demo")
	s, e := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	_, e = s.CreateSite(ctx, store.Site{ID: deployTestSite, Name: "demo", Type: "static", Docroot: filepath.Join(home, "public")}, store.Domain{ID: "domain", FQDN: "demo.test"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetSiteStatus(ctx, deployTestSite, "active"); e != nil {
		t.Fatal(e)
	}
	sealer, _ := secret.New(make([]byte, 32))
	f := &deployFake{t: t, home: home, url: "ssh://git@example.com/repo"}
	a := agent.New()
	a.Cfg.SitesRoot = root
	a.Exec = f
	a.Store = s
	a.Secret = sealer
	a.Jobs = jobs.NewQueue(s, sealer, nil)
	if configured {
		if _, e = deployConfigure(ctx, a, &deployConfigureArgs{deployTestSite, f.url, "main"}); e != nil {
			t.Fatal(e)
		}
	}
	return a, f
}
func codeOf(err error) string {
	var e *rpc.CodedError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
func TestDeployRunWiring(t *testing.T) {
	for _, name := range []string{"deploy.run", "deploy.rollback"} {
		v := rpc.Registry[name]
		if v.MinRole != rpc.Admin || v.Scope != "deploy" || !v.Slow || v.MFA || v.Effectful || v.Exec == nil || v.ObjectKey == nil {
			t.Fatalf("%s: %+v", name, v)
		}
	}
	a, _ := deployFixture(t, false)
	j := &jobs.Job{ID: "job", ObjectKey: "site:" + deployTestSite}
	if got := codeOf((deployService{a}).execute(context.Background(), j, deployTestSite, "main", "")); got != "precondition_failed" {
		t.Fatal(got)
	}
	a, f := deployFixture(t, true)
	j = &jobs.Job{ID: "job", ObjectKey: "site:" + deployTestSite}
	if got := codeOf((deployService{a}).execute(context.Background(), j, deployTestSite, "", testSHA)); got != "invalid_argument" {
		t.Fatal(got)
	}
	identityPath := filepath.Join(f.home, ".novapanel", "deploy", "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityPath, []byte("stale key"), 0600); err != nil {
		t.Fatal(err)
	}
	f.foreign = true
	if got := codeOf((deployService{a}).execute(context.Background(), j, deployTestSite, "main", "")); got != "conflict" {
		t.Fatal(got)
	}
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("identity remains after git error: %v", err)
	}
	f.foreign = false
	f.commands = nil
	f.operations = nil
	if err := (deployService{a}).execute(context.Background(), j, deployTestSite, "main", ""); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, c := range f.commands {
		seen = append(seen, c[0])
	}
	joined := strings.Join(seen, ",")
	if !strings.Contains(joined, "ls-remote,fetch,cat-file,checkout") {
		t.Fatal(joined)
	}
	if strings.Join(f.operations, ",") != "remove,prepare,remove" {
		t.Fatal(f.operations)
	}
	if string(j.Result) != "{\"sha\":\""+testSHA+"\"}" {
		t.Fatal(string(j.Result))
	}
	f.commands, f.operations = nil, nil
	if err := (deployService{a}).execute(context.Background(), j, deployTestSite, "main", ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.commands {
		if c[0] == "ls-remote" {
			t.Fatal("replay resolved a new SHA")
		}
	}
	if strings.Join(f.operations, ",") != "remove,prepare,remove" {
		t.Fatal(f.operations)
	}
	_, _, _, sha, state, e := a.Store.DeployJob(context.Background(), j.ID)
	if e != nil || sha != testSHA || state != "done" {
		t.Fatal(sha, state, e)
	}
}

func TestDeployMissingPinnedCommitFailsClosed(t *testing.T) {
	a, f := deployFixture(t, true)
	f.missingCommit = true
	j := &jobs.Job{ID: "missing-pin", ObjectKey: "site:" + deployTestSite}
	err := (deployService{a}).execute(context.Background(), j, deployTestSite, "main", "")
	if codeOf(err) != "agent_error" {
		t.Fatalf("error %v", err)
	}
	found := false
	for _, c := range f.commands {
		if c[0] == "fetch" {
			found = true
			if c[len(c)-1] != testSHA {
				t.Fatalf("fetch not pinned: %q", c)
			}
		}
		if c[0] == "checkout" {
			t.Fatal("checked out missing commit")
		}
	}
	if !found {
		t.Fatal("missing fetch")
	}
	_, _, _, sha, state, e := a.Store.DeployJob(context.Background(), j.ID)
	if e != nil || sha != testSHA || state != "failed" {
		t.Fatal(sha, state, e)
	}
}

func TestDeployFailedGitRemovesIdentity(t *testing.T) {
	a, f := deployFixture(t, true)
	f.failFetch = true
	path := filepath.Join(f.home, ".novapanel", "deploy", "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	j := &jobs.Job{ID: "failed-fetch", ObjectKey: "site:" + deployTestSite}
	err := (deployService{a}).execute(context.Background(), j, deployTestSite, "main", "")
	if codeOf(err) != "agent_error" {
		t.Fatalf("error %v", err)
	}
	if strings.Join(f.operations, ",") != "remove,prepare,remove" {
		t.Fatal(f.operations)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("identity remains: %v", err)
	}
}

package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	t          *testing.T
	home, url  string
	foreign    bool
	commands   [][]string
	operations []string
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
	if got := codeOf(executeDeploy(context.Background(), a, j, deployTestSite, "main", "")); got != "precondition_failed" {
		t.Fatal(got)
	}
	a, f := deployFixture(t, true)
	j = &jobs.Job{ID: "job", ObjectKey: "site:" + deployTestSite}
	if got := codeOf(executeDeploy(context.Background(), a, j, deployTestSite, "", testSHA)); got != "invalid_argument" {
		t.Fatal(got)
	}
	f.foreign = true
	if got := codeOf(executeDeploy(context.Background(), a, j, deployTestSite, "main", "")); got != "conflict" {
		t.Fatal(got)
	}
	f.foreign = false
	f.commands = nil
	f.operations = nil
	if err := executeDeploy(context.Background(), a, j, deployTestSite, "main", ""); err != nil {
		t.Fatal(err)
	}
	var seen []string
	for _, c := range f.commands {
		seen = append(seen, c[0])
	}
	joined := strings.Join(seen, ",")
	if !strings.Contains(joined, "ls-remote,fetch,checkout") {
		t.Fatal(joined)
	}
	if strings.Join(f.operations, ",") != "prepare,remove" {
		t.Fatal(f.operations)
	}
	if string(j.Result) != "{\"sha\":\""+testSHA+"\"}" {
		t.Fatal(string(j.Result))
	}
	_, _, _, sha, state, e := a.Store.DeployJob(context.Background(), j.ID)
	if e != nil || sha != testSHA || state != "done" {
		t.Fatal(sha, state, e)
	}
}

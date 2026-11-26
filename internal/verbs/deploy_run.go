package verbs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"novapanel/internal/agent"
	"novapanel/internal/deploy"
	runner "novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/platform"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
)

type deployRunArgs struct {
	SiteID string `json:"site_id"`
	Ref    string `json:"ref"`
}
type deployRollbackArgs struct {
	SiteID string `json:"site_id"`
	SHA    string `json:"sha"`
}

func (v *deployRunArgs) Validate() error {
	if err := (&siteIDArgs{v.SiteID}).Validate(); err != nil {
		return err
	}
	if !validBranch(v.Ref) {
		return rpc.Errf("invalid_argument", "Invalid deploy ref")
	}
	return nil
}
func fullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	b, e := hex.DecodeString(s)
	return e == nil && len(b)*2 == len(s) && strings.ToLower(s) == s
}
func (v *deployRollbackArgs) Validate() error {
	if err := (&siteIDArgs{v.SiteID}).Validate(); err != nil {
		return err
	}
	if !fullSHA(v.SHA) {
		return rpc.Errf("invalid_argument", "Invalid deploy SHA")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "deploy.run", MinRole: rpc.Admin, Scope: "deploy", Slow: true, NewArgs: func() any { return &deployRunArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*deployRunArgs).SiteID }, Run: deployRun, Exec: deployRunExec})
	rpc.Register(rpc.Verb{Name: "deploy.rollback", MinRole: rpc.Admin, Scope: "deploy", Slow: true, NewArgs: func() any { return &deployRollbackArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*deployRollbackArgs).SiteID }, Run: deployRollback, Exec: deployRollbackExec})
}
func deployRun(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*deployRunArgs)
	return a.Jobs.Enqueue(ctx, "deploy.run", v, "site:"+v.SiteID)
}
func deployRollback(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*deployRollbackArgs)
	return a.Jobs.Enqueue(ctx, "deploy.rollback", v, "site:"+v.SiteID)
}
func deployRunExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v deployRunArgs
	if json.Unmarshal(j.Args, &v) != nil {
		return rpc.Errf("invalid_argument", "Invalid deploy arguments")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	return (deployService{a}).execute(ctx, j, v.SiteID, v.Ref, "")
}
func deployRollbackExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v deployRollbackArgs
	if json.Unmarshal(j.Args, &v) != nil {
		return rpc.Errf("invalid_argument", "Invalid rollback arguments")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	return (deployService{a}).execute(ctx, j, v.SiteID, "", v.SHA)
}
func deployHelper(ctx context.Context, r runner.Runner, home, op string, key []byte) error {
	program, err := os.Executable()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(deploy.Request{Home: home, Operation: op, Key: key})
	if err != nil {
		return err
	}
	defer clear(payload)
	_, _, code, err := r.Run(ctx, program, []string{"__deploy"}, runner.RunOpts{Stdin: bytes.NewReader(payload)})
	if err != nil || code != 0 {
		return rpc.Errf("agent_error", "Deploy state operation failed")
	}
	return nil
}

type deployService struct{ a *agent.Agent }

func (d deployService) execute(ctx context.Context, j *jobs.Job, siteID, ref, rollback string) (ret error) {
	a := d.a
	if j.ObjectKey != "site:"+siteID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	cfg, err := a.Store.DeployConfig(ctx, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return rpc.Errf("precondition_failed", "Configure deploy first")
	}
	if err != nil {
		return err
	}
	if rollback != "" {
		ok, e := a.Store.HasDeploySHA(ctx, siteID, rollback)
		if e != nil {
			return e
		}
		if !ok {
			return rpc.Errf("invalid_argument", "Unknown deploy SHA")
		}
	}
	oldSite, oldURL, oldBranch, sha, _, err := a.Store.DeployJob(ctx, j.ID)
	exists := err == nil
	replay := exists
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if exists && oldSite != siteID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}

	if exists {
		cfg.RepoURL, cfg.Branch = oldURL, oldBranch
	}
	defer func() {
		if exists && ret != nil {
			if e := a.Store.FinishDeploy(context.Background(), j.ID, "failed"); e != nil {
				ret = jobs.ErrRetry
			}
		}
	}()
	site, err := a.Store.SiteByID(ctx, siteID)
	if err != nil {
		return siteError(err)
	}
	if site.Status != "active" {
		return rpc.Errf("precondition_failed", "Site unavailable")
	}
	home := filepath.Join(a.Cfg.SitesRoot, site.Name)
	if site.Docroot != filepath.Join(home, "public") {
		return rpc.Errf("precondition_failed", "Invalid site docroot")
	}
	uid, gid, err := php.ResolveUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID)
	if err != nil {
		return err
	}
	sr, err := platform.NewSiteRunner(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, uid, gid)
	if err != nil {
		return rpc.Errf("precondition_failed", "Site account unavailable")
	}
	var identity []byte
	if strings.HasPrefix(cfg.RepoURL, "ssh://") {
		sealed, e := a.Store.DeployKey(ctx, siteID)
		if e != nil {
			return e
		}
		seed, e := a.Secret.Open(secret.Sealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
		if e != nil {
			return rpc.Errf("agent_error", "Deploy key unavailable")
		}
		defer clear(seed)
		if len(seed) != ed25519.SeedSize {
			return rpc.Errf("agent_error", "Deploy key unavailable")
		}
		key := ed25519.NewKeyFromSeed(seed)
		defer clear(key)
		block, e := ssh.MarshalPrivateKey(key, "")
		if e != nil {
			return rpc.Errf("agent_error", "Deploy key unavailable")
		}
		identity = pem.EncodeToMemory(block)
		clear(block.Bytes)
		defer clear(identity)
	}
	defer func() {
		if e := deployHelper(context.Background(), sr, home, "remove", nil); e != nil && ret == nil {
			ret = e
		}
	}()
	if err = deployHelper(ctx, sr, home, "prepare", identity); err != nil {
		return err
	}
	dir := filepath.Join(home, ".novapanel", "deploy")
	gitdir := filepath.Join(dir, "repo.git")
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0"}
	if len(identity) > 0 {
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+filepath.Join(dir, "id_ed25519")+" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile="+filepath.Join(dir, "known_hosts")+" -o BatchMode=yes")
	}
	git := func(args ...string) ([]byte, int, error) {
		base := []string{"--git-dir=" + gitdir, "--work-tree=" + site.Docroot, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}
		out, _, code, e := sr.Run(ctx, "/usr/bin/git", append(base, args...), runner.RunOpts{Env: env, Dir: home, MaxOutput: 65536})
		return out, code, e
	}
	fail := func() error { return rpc.Errf("agent_error", "Git deployment failed") }
	if _, e := os.Lstat(gitdir); os.IsNotExist(e) {
		if _, c, e := git("init"); e != nil || c != 0 {
			return fail()
		}
	} else if e != nil {
		return fail()
	}
	if _, c, e := git("rev-parse", "--git-dir"); e != nil || c != 0 {
		return rpc.Errf("conflict", "Invalid deploy repository")
	}
	out, c, e := git("remote", "get-url", "origin")
	if c == 2 {
		if _, c, e = git("remote", "add", "origin", cfg.RepoURL); e != nil || c != 0 {
			return fail()
		}
	} else if e != nil || c != 0 {
		return fail()
	} else if strings.TrimSpace(string(out)) != cfg.RepoURL {
		return rpc.Errf("conflict", "Deploy origin differs")
	}
	if !exists {
		if rollback != "" {
			sha = rollback
		} else {
			out, c, e = git("ls-remote", "--exit-code", cfg.RepoURL, ref, ref+"^{}")
			if e != nil || c != 0 {
				return fail()
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) > 2 {
				return fail()
			}
			for _, line := range lines {
				f := strings.Fields(line)
				if len(f) != 2 || !fullSHA(f[0]) {
					return fail()
				}
				if sha == "" || strings.HasSuffix(f[1], "^{}") {
					sha = f[0]
				}
			}
			if sha == "" {
				return fail()
			}
		}
		if err = a.Store.InsertDeploy(ctx, j.ID, siteID, cfg.RepoURL, cfg.Branch, sha, a.Now()); err != nil {
			return err
		}
		exists = true
	}
	present := false
	if rollback != "" || replay {
		_, c, e = git("cat-file", "-e", sha+"^{commit}")
		present = e == nil && c == 0
	}
	if !present {
		if _, c, e = git("fetch", "--depth", "50", "origin", sha); e != nil || c != 0 {
			fetchRef := ref
			if fetchRef == "" {
				fetchRef = cfg.Branch
			}
			if _, c, e = git("fetch", "--depth", "50", "origin", fetchRef); e != nil || c != 0 {
				return fail()
			}
		}
		if _, c, e = git("cat-file", "-e", sha+"^{commit}"); e != nil || c != 0 {
			return rpc.Errf("agent_error", "Pinned deploy commit unavailable; ref may have moved, retry deployment")
		}
	}
	if _, c, e = git("checkout", "--detach", sha); e != nil || c != 0 {
		return fail()
	}
	if err = a.Store.FinishDeploy(ctx, j.ID, "done"); err != nil {
		return jobs.ErrRetry
	}
	j.Result, _ = json.Marshal(map[string]string{"sha": sha})
	return nil
}

// deployUserRunner runs the teardown helper as the proven site account.
type deployUserRunner struct {
	runner   runner.Runner
	uid, gid uint32
}

func (r deployUserRunner) Run(ctx context.Context, prog string, args []string, opts runner.RunOpts) ([]byte, []byte, int, error) {
	opts.Uid, opts.Gid, opts.Groups = r.uid, r.gid, []uint32{}
	return r.runner.Run(ctx, prog, args, opts)
}

package verbs

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

type deployConfigureArgs struct {
	SiteID  string `json:"site_id"`
	RepoURL string `json:"repo_url"`
	Branch  string `json:"branch"`
}
type deployHistoryArgs struct {
	SiteID string `json:"site_id"`
	Limit  int    `json:"limit,omitempty"`
}

func validBranch(s string) bool {
	if len(s) == 0 || len(s) > 255 || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.Contains(s, "//") || s == "@" || strings.ContainsAny(s, " ~^:?*[\\") {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	for _, part := range strings.Split(s, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
func (v *deployConfigureArgs) Validate() error {
	if err := (&siteIDArgs{v.SiteID}).Validate(); err != nil {
		return err
	}
	if (&validate.V{}).GitURL(v.RepoURL) != nil || !validBranch(v.Branch) {
		return rpc.Errf("invalid_argument", "Invalid deploy configuration")
	}
	return nil
}
func (v *deployHistoryArgs) Validate() error {
	if v.Limit < 0 {
		return rpc.Errf("invalid_argument", "Invalid limit")
	}
	return (&siteIDArgs{v.SiteID}).Validate()
}
func init() {
	rpc.Register(rpc.Verb{Name: "deploy.configure", MinRole: rpc.Admin, Scope: "deploy", NewArgs: func() any { return &deployConfigureArgs{} }, Run: deployConfigure})
	rpc.Register(rpc.Verb{Name: "deploy.status", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "deploy", NewArgs: func() any { return &siteIDArgs{} }, Run: deployStatus})
	rpc.Register(rpc.Verb{Name: "deploy.history", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "deploy", NewArgs: func() any { return &deployHistoryArgs{} }, Run: deployHistory})
}
func deployConfigure(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*deployConfigureArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if a.Jobs == nil || a.Store == nil || a.Secret == nil {
		return nil, rpc.Errf("agent_error", "Deploy unavailable")
	}
	if a.Validate != nil && a.Validate.GitURL(v.RepoURL) != nil {
		return nil, rpc.Errf("invalid_argument", "Invalid deploy repository")
	}
	unlock, err := a.Jobs.LockSite(ctx, v.SiteID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	site, err := a.Store.SiteByID(ctx, v.SiteID)
	if err != nil {
		return nil, siteError(err)
	}
	if site.Status != "active" {
		return nil, rpc.Errf("precondition_failed", "Site unavailable")
	}
	cfg, err := a.Store.DeployConfig(ctx, v.SiteID)
	if err == nil {
		if err = a.Store.UpdateDeployConfig(ctx, v.SiteID, v.RepoURL, v.Branch, a.Now()); err != nil {
			return nil, err
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		pub, private, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return nil, e
		}
		defer clear(private)
		key, e := ssh.NewPublicKey(pub)
		if e != nil {
			return nil, e
		}
		seed := private.Seed()
		defer clear(seed)
		sealed, e := a.Secret.Seal(seed)
		if e != nil {
			return nil, e
		}
		cfg = store.DeployConfig{SiteID: v.SiteID, RepoURL: v.RepoURL, Branch: v.Branch, PublicKey: string(ssh.MarshalAuthorizedKey(key)), Created: a.Now(), Updated: a.Now()}
		if e = a.Store.CreateDeployConfig(ctx, cfg, store.ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}); e != nil {
			return nil, e
		}
	} else {
		return nil, err
	}
	return struct {
		PublicKey string `json:"deploy_pubkey"`
	}{cfg.PublicKey}, nil
}
func deployStatus(ctx context.Context, a *agent.Agent, args any) (any, error) {
	cfg, err := a.Store.DeployConfig(ctx, args.(*siteIDArgs).SiteID)
	if err != nil {
		return nil, siteError(err)
	}
	rows, err := a.Store.DeployHistory(ctx, cfg.SiteID, 1, true)
	if err != nil {
		return nil, err
	}
	v := struct {
		SHA       string `json:"current_sha"`
		State     string `json:"state"`
		LastRun   int64  `json:"last_run"`
		PublicKey string `json:"deploy_pubkey"`
	}{State: "not_deployed", PublicKey: cfg.PublicKey}
	if len(rows) != 0 {
		v.SHA, v.State, v.LastRun = rows[0].SHA, rows[0].State, rows[0].Created
	}
	return v, nil
}
func deployHistory(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*deployHistoryArgs)
	limit := v.Limit
	if limit == 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := a.Store.DeployHistory(ctx, v.SiteID, limit, false)
	if err != nil {
		return nil, err
	}
	return struct {
		Deploys []store.Deploy `json:"deploys"`
	}{rows}, nil
}

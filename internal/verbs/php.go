package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"path/filepath"
	"time"
)

func init() {
	rpc.Register(rpc.Verb{Name: "php.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &emptyArgs{} }, Run: phpList})
	rpc.Register(rpc.Verb{Name: "php.set", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &phpSetArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*phpSetArgs).request.SiteID }, Run: phpSet, Exec: phpSetExec})
}

type phpSetArgs struct{ request php.SetRequest }

func (v *phpSetArgs) UnmarshalJSON(raw []byte) error {
	request, err := php.DecodeSetRequest(raw)
	if err != nil {
		return err
	}
	v.request = request
	return nil
}

func phpSet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	request := args.(*phpSetArgs).request
	return a.Jobs.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: request.SiteID, PHP: &request}, nil)
}

// No default dependencies are installed until startup recovery owns snapshots.
func phpSetExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	if a == nil || j == nil || a.Store == nil || a.Jobs == nil || a.Nginx == nil || a.PHP == nil || a.PHP.Pools == nil || a.PHP.ResolveIdentity == nil || a.PHP.RecoveryRoot == "" || a.Nginx.Runner == nil || a.Nginx.Probe == nil || a.PHP.Pools.Runner == nil || a.PHP.Pools.Probe == nil {
		return rpc.Errf("unsupported", "PHP execution unavailable")
	}
	op, err := a.Store.LoadOperation(ctx, j.ID)
	if err != nil {
		return err
	}
	if op.Kind != store.PHPSet || op.Identity.Site == nil || op.Identity.PHP == nil || j.ObjectKey != "site:"+op.SiteID || op.Checkpoint != "prepared" {
		return rpc.Errf("precondition_failed", "Invalid PHP operation")
	}
	check := func(c context.Context) error { return a.Jobs.CheckPHPEffect(c, j.ID, op.SiteID) }
	if err = check(ctx); err != nil {
		return err
	}
	s := *op.Identity.Site
	uid, gid, err := a.PHP.ResolveIdentity(ctx, s)
	if err != nil {
		return err
	}
	domains, err := a.Store.ListDomains(ctx, op.SiteID)
	if err != nil {
		return err
	}
	redirects, err := a.Store.ListRedirects(ctx, op.SiteID)
	if err != nil {
		return err
	}
	d := nginx.Desired{Name: s.Name, Docroot: s.Docroot, ACMERoot: filepath.Join(a.Cfg.ACMEWebroot, s.Name), Maintenance: s.Maintenance, ForceHTTPS: s.ForceHTTPS, HSTSMaxAge: s.HSTSMaxAge}
	for _, v := range domains {
		d.Domains = append(d.Domains, v.FQDN)
	}
	for _, v := range redirects {
		d.Redirects = append(d.Redirects, nginx.Redirect{Source: v.Source, Target: v.Target, Code: v.Code})
	}
	if s.CertSource != "none" {
		_, primary := sslNames(domains)
		d.TLS, err = nginx.LoadTLS(a.Cfg.TLSDir, primary, tlsStaging(domains), time.UnixMilli(a.Now()))
		if err != nil {
			return err
		}
	}
	p := op.Identity.PHP
	d.PHP = &php.Desired{Name: s.Name, Type: s.Type, Version: p.DesiredVersion, UID: uid, GID: gid, Limits: &p.DesiredLimits}
	old := d
	oldPHP := *d.PHP
	oldPHP.Version, oldPHP.Limits = p.OriginalVersion, &p.OriginalLimits
	old.PHP = &oldPHP
	binding := nginx.PHPDurableBinding{Root: a.PHP.RecoveryRoot, JobID: j.ID, SiteID: op.SiteID, Check: check}
	// Terminal metadata and its audit are owned by Queue.finish, not this callback.
	commit := func(c context.Context) error {
		if err := check(c); err != nil {
			return err
		}
		if err := a.Store.CheckpointOperation(c, j.ID, "prepared", "config_published"); err != nil {
			return err
		}
		if err := check(c); err != nil {
			return err
		}
		return a.Store.CheckpointOperation(c, j.ID, "config_published", "verified")
	}
	if err = a.Nginx.ApplyPHPDurable(ctx, a.PHP.Pools, d, binding, commit, func(c context.Context) error {
		if err := a.PHP.Pools.Probe(c, oldPHP); err != nil {
			return err
		}
		return a.Nginx.Probe(c, old)
	}); err != nil {
		return err
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

var _ json.Unmarshaler = (*phpSetArgs)(nil)

// phpList reports the supported version policy, not host package discovery.
// Installation and service readiness remain mutation preconditions.
func phpList(context.Context, *agent.Agent, any) (any, error) {
	return struct {
		Versions []string `json:"versions"`
		Default  string   `json:"default"`
	}{[]string{"8.3"}, "8.3"}, nil
}

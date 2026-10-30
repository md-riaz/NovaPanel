package verbs

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

type sslACMEArgs struct {
	SiteID  string `json:"site_id"`
	Staging *bool  `json:"staging,omitempty"`
}

func (v *sslACMEArgs) Validate() error { return (&siteIDArgs{v.SiteID}).Validate() }
func init() {
	rpc.Register(rpc.Verb{Name: "ssl.issue", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, Effectful: true, NewArgs: func() any { return &sslACMEArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*sslACMEArgs).SiteID }, Run: sslIssue, Exec: sslIssueExec})
	rpc.Register(rpc.Verb{Name: "ssl.renew", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, Effectful: true, NewArgs: func() any { return &sslACMEArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*sslACMEArgs).SiteID }, Run: sslRenew, Exec: sslRenewExec})
}
func sslIssue(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return (sslACMEState{a.Store, a.Cfg.TLSDir, a.Now}).admit(ctx, a.Jobs, args.(*sslACMEArgs), a.Cfg.ACMEUseStaging, a.Cfg.ACMEEmail, "ssl.issue")
}
func sslRenew(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return (sslACMEState{a.Store, a.Cfg.TLSDir, a.Now}).admit(ctx, a.Jobs, args.(*sslACMEArgs), a.Cfg.ACMEUseStaging, a.Cfg.ACMEEmail, "ssl.renew")
}

type sslACMEState struct {
	store *store.Store
	root  string
	now   func() int64
}

func (s sslACMEState) check(ctx context.Context, v *sslACMEArgs, verb string) (bool, error) {
	if e := v.Validate(); e != nil {
		return false, e
	}
	site, ds, e := (sslState{s.store}).active(ctx, v.SiteID)
	if e != nil {
		return false, e
	}
	names := []string{}
	primary := ""
	for _, d := range ds {
		if strings.HasPrefix(d.FQDN, "*.") {
			return false, rpc.Errf("unsupported", "Wildcard certificates require DNS-01")
		}
		names = append(names, d.FQDN)
		if d.IsPrimary {
			primary = d.FQDN
		}
	}
	if primary == "" {
		return false, rpc.Errf("precondition_failed", "Site has no primary domain")
	}
	if verb == "ssl.renew" && site.CertSource != "automatic" {
		return false, rpc.Errf("precondition_failed", "Only automatic certificates can renew")
	}
	now := time.UnixMilli(s.now())
	installed, e := certs.Load(s.root, primary, names, now)
	if e != nil {
		return false, nil
	}
	// Metadata may only narrow reuse, never establish validity in place of the pair.
	for _, d := range ds {
		if v.Staging == nil || d.CertStaging != *v.Staging {
			return false, nil
		}
	}
	if verb == "ssl.issue" {
		return true, nil
	}
	return installed.NotAfter.After(now.Add(30 * 24 * time.Hour)), nil
}
func (s sslACMEState) admit(ctx context.Context, q *jobs.Queue, v *sslACMEArgs, defaultStaging bool, email, verb string) (any, error) {
	if v.Staging == nil {
		staging := defaultStaging
		v.Staging = &staging
	}
	// Reject unsupported names before config checks or filesystem/network access.
	site, ds, e := (sslState{s.store}).active(ctx, v.SiteID)
	if e != nil {
		return nil, e
	}
	_ = site
	for _, d := range ds {
		if strings.HasPrefix(d.FQDN, "*.") {
			return nil, rpc.Errf("unsupported", "Wildcard certificates require DNS-01")
		}
	}
	if !*v.Staging && email == "" {
		return nil, rpc.Errf("precondition_failed", "Production ACME email is required")
	}
	noop, e := s.check(ctx, v, verb)
	if e != nil {
		return nil, e
	}
	if noop {
		return struct {
			Unchanged bool `json:"unchanged"`
		}{true}, nil
	}
	return q.Enqueue(ctx, verb, v, "site:"+v.SiteID)
}
func sslIssueExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	return (sslACMEState{a.Store, a.Cfg.TLSDir, a.Now}).execute(ctx, j, "ssl.issue")
}
func sslRenewExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	return (sslACMEState{a.Store, a.Cfg.TLSDir, a.Now}).execute(ctx, j, "ssl.renew")
}
func (s sslACMEState) execute(ctx context.Context, j *jobs.Job, verb string) error {
	var v sslACMEArgs
	if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil {
		return rpc.Errf("invalid_argument", "Invalid ACME job")
	}
	if j.Verb != verb || j.ObjectKey != "site:"+v.SiteID || v.Staging == nil {
		return rpc.Errf("precondition_failed", "ACME job parent mismatch")
	}
	noop, e := s.check(ctx, &v, verb)
	if e != nil {
		return e
	}
	if noop {
		return nil
	}
	// Never consume a CA order before durable common TLS/nginx publication exists.
	return rpc.Errf("precondition_failed", "TLS publication recovery is not yet available")
}

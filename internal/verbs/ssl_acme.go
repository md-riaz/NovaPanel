package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"novapanel/internal/acme"
	"path/filepath"
	"strings"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

type sslACMEArgs struct {
	SiteID  string `json:"site_id"`
	Staging *bool  `json:"staging,omitempty"`
}

type sslIssueArgs struct {
	sslACMEArgs
	Wildcard bool `json:"wildcard,omitempty"`
}

func (v *sslIssueArgs) Validate() error {
	if err := v.sslACMEArgs.Validate(); err != nil {
		return err
	}
	if v.Wildcard {
		return rpc.Errf("unsupported", "Wildcard certificates require DNS-01")
	}
	return nil
}

func (v *sslACMEArgs) Validate() error { return (&siteIDArgs{v.SiteID}).Validate() }
func init() {
	rpc.Register(rpc.Verb{Name: "ssl.issue", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, Effectful: true, NewArgs: func() any { return &sslIssueArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*sslIssueArgs).SiteID }, Run: sslIssue, Exec: sslIssueExec, ValidateRecovery: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (tlsStartup{a}).validate(ctx, j) }, Recover: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (tlsStartup{a}).resume(ctx, j) }})
	rpc.Register(rpc.Verb{Name: "ssl.renew", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, Effectful: true, NewArgs: func() any { return &siteIDArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*siteIDArgs).SiteID }, NewJobArgs: func() any { return &sslACMEArgs{} }, JobObjectKey: func(v any) string { return "site:" + v.(*sslACMEArgs).SiteID }, Run: sslRenew, Exec: sslRenewExec, ValidateRecovery: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (tlsStartup{a}).validate(ctx, j) }, Recover: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (tlsStartup{a}).resume(ctx, j) }})
}
func sslIssue(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*sslIssueArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return (sslACMEState{a.Store, a.Cfg.TLSDir, a.Now}).admit(ctx, a.Jobs, &v.sslACMEArgs, a.Cfg.ACMEUseStaging, a.Cfg.ACMEEmail, "ssl.issue")
}
func sslRenew(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return (sslACMEState{a.Store, a.Cfg.TLSDir, a.Now}).admit(ctx, a.Jobs, &sslACMEArgs{SiteID: args.(*siteIDArgs).SiteID}, a.Cfg.ACMEUseStaging, a.Cfg.ACMEEmail, "ssl.renew")
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
	if verb == "ssl.renew" {
		staging, err := s.renewalEnvironment(site, ds)
		if err != nil {
			return false, err
		}
		if v.Staging == nil || *v.Staging != staging {
			return false, rpc.Errf("precondition_failed", "Automatic certificate environment changed")
		}
		if _, err = certs.LoadRenewalSource(s.root, primary, time.UnixMilli(s.now())); err != nil {
			return false, rpc.Errf("precondition_failed", "Installed automatic certificate is unavailable")
		}
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
	staging := defaultStaging
	// Reject unsupported names before config checks or filesystem/network access.
	site, ds, e := (sslState{s.store}).active(ctx, v.SiteID)
	if e != nil {
		return nil, e
	}
	for _, d := range ds {
		if strings.HasPrefix(d.FQDN, "*.") {
			return nil, rpc.Errf("unsupported", "Wildcard certificates require DNS-01")
		}
	}
	if verb == "ssl.renew" {
		staging, e = s.renewalEnvironment(site, ds)
		if e != nil {
			return nil, e
		}
		v.Staging = &staging
	} else if v.Staging == nil {
		v.Staging = &staging
	}
	if !*v.Staging && email == "" {
		return nil, rpc.Errf("precondition_failed", "Production ACME email is required")
	}
	if _, e = s.check(ctx, v, verb); e != nil {
		return nil, e
	}
	return q.Enqueue(ctx, verb, v, "site:"+v.SiteID)
}

func (s sslACMEState) renewalEnvironment(site store.Site, ds []store.Domain) (bool, error) {
	if site.CertSource != "letsencrypt" {
		return false, rpc.Errf("precondition_failed", "Only automatic certificates can renew")
	}
	var primary *store.Domain
	for i := range ds {
		if ds[i].IsPrimary {
			primary = &ds[i]
		}
	}
	if primary == nil || primary.CertState != "issued" || primary.CertNotAfter == nil {
		return false, rpc.Errf("precondition_failed", "Installed automatic certificate metadata is unavailable")
	}
	for _, d := range ds {
		// Newly added, uncertified aliases have no installed provenance yet.
		if d.CertState == "none" {
			continue
		}
		if d.CertState != "issued" || d.CertNotAfter == nil || d.CertStaging != primary.CertStaging || *d.CertNotAfter != *primary.CertNotAfter {
			return false, rpc.Errf("precondition_failed", "Inconsistent automatic certificate provenance")
		}
	}
	return primary.CertStaging, nil
}
func sslIssueExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	return (acmeExecutor{a}).execute(ctx, j, "ssl.issue", nil)
}
func sslRenewExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	return (acmeExecutor{a}).execute(ctx, j, "ssl.renew", nil)
}

type acmeExecutor struct{ a *agent.Agent }

func (x acmeExecutor) execute(ctx context.Context, j *jobs.Job, verb string, transport *http.Client) (result error) {
	defer func() {
		if errors.Is(result, store.ErrACMERateLimited) {
			result = rpc.Errf("rate_limited", "ACME issuance rate limited")
		}
	}()
	a := x.a
	s := sslACMEState{a.Store, a.Cfg.TLSDir, a.Now}
	durable, e := a.Store.GetJob(ctx, j.ID)
	if e != nil {
		return e
	}
	if durable.Verb != verb || durable.ObjectKey != j.ObjectKey || durable.State != "running" {
		return rpc.Errf("precondition_failed", "ACME job parent mismatch")
	}
	j.Args = durable.Args
	if durable.Step == "tls_committed" {
		j.Step = durable.Step
		j.Result = append(json.RawMessage(nil), durable.Result...)
		return nil
	}
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
	intent, e := a.Store.TLSIntent(ctx)
	if e != nil {
		return e
	}
	if noop && len(intent) == 0 {
		return a.Nginx.Transaction(ctx, func(_ *nginx.Transaction) error {
			intent, err := a.Store.TLSIntent(ctx)
			if err != nil {
				return err
			}
			if len(intent) != 0 {
				return rpc.Errf("precondition_failed", "Unresolved TLS recovery intent")
			}
			_, ds, err := (sslState{a.Store}).active(ctx, v.SiteID)
			if err != nil {
				return err
			}
			names, primary := sslNames(ds)
			pair, err := certs.Load(a.Cfg.TLSDir, primary, names, time.UnixMilli(a.Now()))
			if err != nil {
				return err
			}
			var result any = struct {
				NotAfter int64 `json:"not_after"`
			}{pair.NotAfter.UnixMilli()}
			if verb == "ssl.issue" {
				result = struct {
					FQDNs    []string `json:"fqdns"`
					NotAfter int64    `json:"not_after"`
				}{names, pair.NotAfter.UnixMilli()}
			}
			durable.Result, err = json.Marshal(result)
			if err != nil {
				return err
			}
			durable.Step = "tls_committed"
			if err = a.Store.UpdateJob(ctx, durable); err != nil {
				return err
			}
			j.Step, j.Result = durable.Step, append(json.RawMessage(nil), durable.Result...)
			return nil
		})
	}
	env, directory := acme.Staging, a.Cfg.ACMEDirectoryStaging
	if !*v.Staging {
		env, directory = acme.Production, a.Cfg.ACMEDirectoryProd
	}
	return (sslPublication{a}).publishCertificate(ctx, v.SiteID, j, *v.Staging, func(names []string) (*certs.Certificate, error) {
		client, err := acme.NewPersisted(ctx, a.Store, acme.Config{Environment: env, Directory: directory, Email: a.Cfg.ACMEEmail, Sealer: a.Secret, TermsOfServiceAgreed: true, HTTPClient: transport}, a.Now)
		if err != nil {
			return nil, err
		}
		site, ds, err := (sslState{a.Store}).active(ctx, v.SiteID)
		if err != nil {
			return nil, err
		}
		_, primary := sslNames(ds)
		root := filepath.Join(a.Cfg.ACMEWebroot, site.Name)
		if verb == "ssl.issue" {
			return client.Issue(ctx, names, root)
		}
		previous, err := certs.LoadRenewalSource(a.Cfg.TLSDir, primary, time.UnixMilli(a.Now()))
		if err != nil {
			return nil, err
		}
		return client.Renew(ctx, names, root, previous)
	})
}

package verbs

import (
	"context"
	"encoding/json"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
)

type sslUploadArgs struct {
	SiteID  string `json:"site_id"`
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

func (v *sslUploadArgs) Validate() error {
	if err := (&siteIDArgs{v.SiteID}).Validate(); err != nil {
		return err
	}
	if len(v.CertPEM) == 0 || len(v.CertPEM) > certs.MaxCertificateBytes || len(v.KeyPEM) == 0 || len(v.KeyPEM) > certs.MaxKeyBytes {
		return rpc.Errf("invalid_argument", "Invalid TLS certificate or private key")
	}
	return nil
}

type sslAutoArgs struct {
	SiteID  string `json:"site_id"`
	Enabled *bool  `json:"enabled"`
}

func (v *sslAutoArgs) Validate() error {
	if err := (&siteIDArgs{v.SiteID}).Validate(); err != nil {
		return err
	}
	if v.Enabled == nil {
		return rpc.Errf("bad_request", "enabled is required")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "ssl.status", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &siteIDArgs{} }, Run: sslStatus})
	rpc.Register(rpc.Verb{Name: "ssl.auto_set", MinRole: rpc.Admin, Scope: "sites:write", NewArgs: func() any { return &sslAutoArgs{} }, Run: sslAutoSet})
	rpc.Register(rpc.Verb{Name: "ssl.upload", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, MFA: true, NewArgs: func() any { return &sslUploadArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*sslUploadArgs).SiteID }, Run: sslUpload, Exec: sslUploadExec})
}

type sslState struct{ store *store.Store }

func (s sslState) active(ctx context.Context, sid string) (store.Site, []store.Domain, error) {
	site, err := s.store.SiteByID(ctx, sid)
	if err != nil {
		return site, nil, siteError(err)
	}
	if site.Status != "active" {
		return site, nil, rpc.Errf("precondition_failed", "Site must be active")
	}
	domains, err := s.store.ListDomains(ctx, sid)
	if err != nil {
		return site, nil, err
	}
	if len(domains) == 0 {
		return site, nil, rpc.Errf("precondition_failed", "Site has no domains")
	}
	return site, domains, nil
}
func sslAutoSet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*sslAutoArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if _, _, err := (sslState{a.Store}).active(ctx, v.SiteID); err != nil {
		return nil, err
	}
	if err := a.Store.SetSSLAuto(ctx, v.SiteID, *v.Enabled); err != nil {
		return nil, siteError(err)
	}
	return struct{}{}, nil
}
func sslUpload(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*sslUploadArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	_, ds, err := (sslState{a.Store}).active(ctx, v.SiteID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ds))
	for _, d := range ds {
		names = append(names, d.FQDN)
	}
	if _, err = certs.Validate([]byte(v.CertPEM), []byte(v.KeyPEM), names, time.UnixMilli(a.Now())); err != nil {
		return nil, err
	}
	return a.Jobs.EnqueueSealed(ctx, "ssl.upload", v, "site:"+v.SiteID)
}
func sslUploadExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	durable, err := a.Store.GetJob(ctx, j.ID)
	if err != nil {
		return err
	}
	if j.Verb != "ssl.upload" || durable.Verb != "ssl.upload" || durable.ObjectKey != j.ObjectKey {
		return rpc.Errf("precondition_failed", "TLS job parent mismatch")
	}
	if durable.Step == "tls_committed" {
		j.Step = durable.Step
		j.Result = append(json.RawMessage(nil), durable.Result...)
		return nil
	}
	raw, err := a.Jobs.OpenInput(j)
	if err != nil {
		return err
	}
	defer clear(raw)
	var v sslUploadArgs
	if err = json.Unmarshal(raw, &v); err != nil {
		return rpc.Errf("invalid_argument", "Invalid sealed TLS input")
	}
	if err = v.Validate(); err != nil {
		return err
	}
	if j.Verb != "ssl.upload" || j.ObjectKey != "site:"+v.SiteID {
		return rpc.Errf("precondition_failed", "TLS job parent mismatch")
	}
	return (sslPublication{a}).publish(ctx, v, j)
}

type sslDomainStatus struct {
	FQDN      string `json:"fqdn"`
	CertState string `json:"cert_state"`
	NotAfter  *int64 `json:"not_after"`
}

func sslStatus(ctx context.Context, a *agent.Agent, args any) (any, error) {
	sid := args.(*siteIDArgs).SiteID
	site, err := a.Store.SiteByID(ctx, sid)
	if err != nil {
		return nil, siteError(err)
	}
	ds, err := a.Store.ListDomains(ctx, sid)
	if err != nil {
		return nil, err
	}
	primary := ""
	for _, d := range ds {
		if d.IsPrimary {
			primary = d.FQDN
		}
	}
	var installed *certs.Certificate
	if site.CertSource != "none" && primary != "" {
		installed, err = certs.Load(a.Cfg.TLSDir, primary, []string{primary}, time.UnixMilli(a.Now()))
		if err != nil {
			installed = nil
		}
	}
	out := []sslDomainStatus{}
	for _, d := range ds {
		state := sslDomainStatus{FQDN: d.FQDN, CertState: "none"}
		if installed != nil && installed.Covers(d.FQDN) {
			n := installed.NotAfter.UnixMilli()
			state.CertState = "issued"
			state.NotAfter = &n
		} else if d.CertNotAfter != nil && *d.CertNotAfter <= a.Now() {
			state.CertState = "expired"
			state.NotAfter = d.CertNotAfter
		}
		out = append(out, state)
	}
	return struct {
		Source     string            `json:"source"`
		SSLAuto    bool              `json:"ssl_auto"`
		ForceHTTPS bool              `json:"force_https"`
		Domains    []sslDomainStatus `json:"domains"`
	}{site.CertSource, site.SSLAuto, site.ForceHTTPS, out}, nil
}

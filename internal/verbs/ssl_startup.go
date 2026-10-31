package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	certs "novapanel/internal/tls"
)

type tlsStartup struct{ a *agent.Agent }

func (s tlsStartup) validate(ctx context.Context, j *jobs.Job) error {
	a := s.a
	raw, err := a.Store.TLSIntent(ctx)
	if err != nil {
		return err
	}
	if j.Step == "tls_committed" && len(raw) == 0 {
		return nil
	}
	var intent tlsIntent
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&intent) != nil || d.Decode(new(any)) != io.EOF || (intent.Version != 1 && intent.Version != 2) || intent.JobID != j.ID || !id.Valid(intent.JobID) || !id.Valid(intent.SiteID) || j.ObjectKey != "site:"+intent.SiteID || a.Nginx == nil || intent.NginxRoot != a.Nginx.Root || intent.Token.Root != a.Cfg.TLSDir || !id.Valid(intent.Token.Version) || (intent.Token.Previous != "" && !id.Valid(intent.Token.Previous)) || intent.Token.Version == intent.Token.Previous {
		return rpc.Errf("precondition_failed", "Invalid TLS recovery binding")
	}
	if j.Verb == "site.update" {
		var v siteUpdateArgs
		if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil || v.SiteID != intent.SiteID {
			return rpc.Errf("precondition_failed", "TLS recovery input mismatch")
		}
		site, ds, err := (sslState{a.Store}).active(ctx, intent.SiteID)
		if err != nil {
			return err
		}
		names, primary := sslNames(ds)
		if intent.Config.Name != site.Name || !equalNames(names, intent.Domains) || !validPrimaryIntent(intent, site, ds, v, a.Cfg.TLSDir, primary) || (v.HSTSMaxAge != nil && j.Role != "owner") {
			return rpc.Errf("precondition_failed", "TLS primary recovery binding changed")
		}
		if err = checkRetainedPrimary(*intent.OldToken); err != nil {
			return err
		}
		return certs.CheckRecovery(intent.Token)
	}
	if intent.Version != 1 || intent.Promotion != nil || intent.OldToken != nil {
		return rpc.Errf("precondition_failed", "Invalid SSL recovery version")
	}
	var args struct {
		SiteID  string `json:"site_id"`
		Staging *bool  `json:"staging"`
	}
	input := j.Args
	if j.Verb == "ssl.upload" {
		input, err = a.Jobs.OpenInput(j)
		if err != nil {
			return err
		}
		defer clear(input)
	}
	if json.Unmarshal(input, &args) != nil || args.SiteID != intent.SiteID || (j.Verb != "ssl.upload" && args.Staging == nil) {
		return rpc.Errf("precondition_failed", "TLS recovery input mismatch")
	}
	site, ds, err := (sslState{a.Store}).active(ctx, intent.SiteID)
	if err != nil {
		return err
	}
	names, primary := sslNames(ds)
	if intent.Config.Name != site.Name || intent.Token.Primary != primary || !equalNames(names, intent.Domains) {
		return rpc.Errf("precondition_failed", "TLS recovery binding changed")
	}
	// Check retained version files and pointer without changing either.
	return certs.CheckRecovery(intent.Token)
}

func (s tlsStartup) resume(ctx context.Context, j *jobs.Job) error {
	a := s.a
	if err := s.validate(ctx, j); err != nil {
		return err
	}
	if j.Step == "tls_committed" {
		return nil
	}
	if j.Verb == "site.update" {
		var v siteUpdateArgs
		if err := json.Unmarshal(j.Args, &v); err != nil {
			return err
		}
		// Only replay the validated journal, never the general site update executor.
		return (sslPublication{a}).publication(ctx, v.SiteID, j, false, nil, &v)
	}
	var v struct {
		SiteID  string `json:"site_id"`
		Staging *bool  `json:"staging"`
	}
	raw := j.Args
	if j.Verb == "ssl.upload" {
		var err error
		raw, err = a.Jobs.OpenInput(j)
		if err != nil {
			return err
		}
		defer clear(raw)
	}
	if json.Unmarshal(raw, &v) != nil {
		return rpc.Errf("precondition_failed", "Invalid TLS recovery input")
	}
	staging := false
	if j.Verb != "ssl.upload" {
		if v.Staging == nil {
			return rpc.Errf("precondition_failed", "Missing TLS environment")
		}
		staging = *v.Staging
	}
	return (sslPublication{a}).publishCertificate(ctx, v.SiteID, j, staging, func([]string) (*certs.Certificate, error) {
		return nil, rpc.Errf("precondition_failed", "TLS recovery cannot acquire certificates")
	})
}

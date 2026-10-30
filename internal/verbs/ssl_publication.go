package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/render/nginx"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	certs "novapanel/internal/tls"
	"path/filepath"
	"time"
)

type tlsIntent struct {
	Version   int            `json:"version"`
	JobID     string         `json:"job_id"`
	SiteID    string         `json:"site_id"`
	NginxRoot string         `json:"nginx_root"`
	Domains   []string       `json:"domains"`
	Token     certs.Recovery `json:"token"`
	Config    nginx.Snapshot `json:"config"`
}

type sslPublication struct{ a *agent.Agent }

func (s sslPublication) publish(ctx context.Context, v sslUploadArgs, j *jobs.Job) (result error) {
	return s.publishCertificate(ctx, v.SiteID, j, false, func(names []string) (*certs.Certificate, error) {
		return certs.Validate([]byte(v.CertPEM), []byte(v.KeyPEM), names, time.UnixMilli(s.a.Now()))
	})
}

func (s sslPublication) publishCertificate(ctx context.Context, siteID string, j *jobs.Job, staging bool, acquire func([]string) (*certs.Certificate, error)) (result error) {
	a := s.a
	defer func() {
		if recover() != nil {
			result = rpc.Errf("interrupted", "TLS publication requires recovery")
		}
		if result != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			raw, err := a.Store.TLSIntent(recovery)
			if err != nil || len(raw) > 0 {
				result = rpc.Errf("interrupted", "TLS publication requires recovery")
			}
		}
	}()
	if a.Nginx == nil {
		return rpc.Errf("agent_error", "Nginx unavailable")
	}
	return a.Nginx.Transaction(ctx, func(tx *nginx.Transaction) error {
		durable, err := a.Store.GetJob(ctx, j.ID)
		if err != nil {
			return err
		}
		if durable.Verb != j.Verb || (j.Verb != "ssl.upload" && j.Verb != "ssl.issue" && j.Verb != "ssl.renew") || durable.ObjectKey != "site:"+siteID {
			return rpc.Errf("precondition_failed", "TLS job parent mismatch")
		}
		if durable.Step == "tls_committed" {
			j.Step = durable.Step
			j.Result = append(json.RawMessage(nil), durable.Result...)
			return nil
		}
		var replay *tlsIntent
		raw, err := a.Store.TLSIntent(ctx)
		if err != nil {
			return err
		}
		if len(raw) > 0 {
			var intent tlsIntent
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if dec.Decode(&intent) != nil || dec.Decode(new(any)) != io.EOF || intent.Version != 1 || intent.JobID != j.ID || intent.SiteID != siteID || intent.Token.Root != a.Cfg.TLSDir || intent.NginxRoot != a.Nginx.Root || len(intent.Domains) == 0 {
				return rpc.Errf("precondition_failed", "Unresolved TLS recovery intent")
			}
			site, ds, e := (sslState{a.Store}).active(ctx, siteID)
			if e != nil {
				return e
			}
			names, primary := sslNames(ds)
			if intent.Config.Name != site.Name || intent.Token.Primary != primary || !equalNames(names, intent.Domains) {
				return rpc.Errf("precondition_failed", "TLS recovery binding changed")
			}
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if e = certs.Restore(intent.Token); e != nil {
				return e
			}
			if e = tx.Restore(recovery, intent.Config, true); e != nil {
				return e
			}
			replay = &intent
		}
		site, ds, err := (sslState{a.Store}).active(ctx, siteID)
		if err != nil {
			return err
		}
		names, primary := sslNames(ds)
		var certificate *certs.Certificate
		if replay != nil {
			// The retained intent owns this pointer while the shared nginx lock is held.
			if err = certs.Publish(replay.Token); err != nil {
				return err
			}
			certificate, err = certs.Load(a.Cfg.TLSDir, primary, names, time.UnixMilli(a.Now()))
			if restoreErr := certs.Restore(replay.Token); restoreErr != nil {
				return restoreErr
			}
		} else {
			certificate, err = acquire(names)
		}
		if err != nil {
			return err
		}
		rs, err := a.Store.ListRedirects(ctx, siteID)
		if err != nil {
			return err
		}
		snapshot, err := tx.Snapshot(site.Name)
		if err != nil {
			return err
		}
		var token certs.Recovery
		if replay != nil {
			token = replay.Token
			snapshot = replay.Config
		} else {
			token, err = certs.Stage(a.Cfg.TLSDir, primary, certificate)
			if err != nil {
				return err
			}
		}
		intent := tlsIntent{Version: 1, JobID: j.ID, SiteID: siteID, NginxRoot: a.Nginx.Root, Domains: names, Token: token, Config: snapshot}
		if replay == nil {
			raw, err = json.Marshal(intent)
			if err != nil {
				return err
			}
			if err = a.Store.BeginTLSIntent(ctx, raw); err != nil {
				return err
			}
		}
		rollback := func(recovery context.Context, reload bool) error {
			if e := certs.Restore(token); e != nil {
				return e
			}
			if e := tx.Restore(recovery, snapshot, reload); e != nil {
				return e
			}
			return a.Store.ClearTLSIntent(recovery, raw)
		}
		if err = certs.Publish(token); err != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if e := rollback(recovery, false); e != nil {
				return e
			}
			return err
		}
		state, err := nginx.LoadTLS(a.Cfg.TLSDir, primary, staging, time.UnixMilli(a.Now()))
		if err != nil {
			recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if e := rollback(recovery, false); e != nil {
				return e
			}
			return err
		}
		d := nginx.Desired{Name: site.Name, Domains: names, Docroot: site.Docroot, ACMERoot: filepath.Join(a.Cfg.ACMEWebroot, site.Name), Maintenance: site.Maintenance, ForceHTTPS: site.ForceHTTPS, HSTSMaxAge: site.HSTSMaxAge, TLS: state, ForceReload: true}
		for _, r := range rs {
			d.Redirects = append(d.Redirects, nginx.Redirect{Source: r.Source, Target: r.Target, Code: r.Code})
		}
		return tx.Apply(ctx, d, rollback, func(ctx context.Context) error {
			if err := a.Store.CommitTLS(ctx, siteID, j.ID, j.Verb, staging, certificate.NotAfter.UnixMilli(), raw); err != nil {
				return err
			}
			durable, err = a.Store.GetJob(ctx, j.ID)
			if err != nil {
				return err
			}
			j.Step = "tls_committed"
			j.Result = append(json.RawMessage(nil), durable.Result...)
			return nil
		})
	})
}
func sslNames(ds []store.Domain) ([]string, string) {
	names := make([]string, 0, len(ds))
	primary := ""
	for _, d := range ds {
		names = append(names, d.FQDN)
		if d.IsPrimary {
			primary = d.FQDN
		}
	}
	return names, primary
}
func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

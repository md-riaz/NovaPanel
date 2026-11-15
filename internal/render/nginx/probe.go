package nginx

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"time"
)

// HTTPProbe retains the HTTP-only constructor. TLS callers must use StateProbe.
func HTTPProbe(endpoint string) func(context.Context, Desired) error {
	return StateProbe(endpoint, "", nil)
}

// StateProbe connects only to fixed loopback endpoints. TLS uses the site's host
// for both SNI and Host, without consulting its DNS. A LoadTLS-approved state pins
// the exact installed leaf and checks hostname and dates, including private CAs.
// Without an approved identity the cloned TLS policy retains system-root defaults.
func StateProbe(httpEndpoint, httpsEndpoint string, tlsConfig *tls.Config) func(context.Context, Desired) error {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if tlsConfig != nil {
		config = tlsConfig.Clone()
	}
	return func(ctx context.Context, d Desired) error {
		_, err := probeEndpoint(httpEndpoint, "http")
		if err != nil {
			return err
		}
		var secure *url.URL
		if d.TLS != nil {
			secure, err = probeEndpoint(httpsEndpoint, "https")
			if err != nil {
				return err
			}
		}
		for _, host := range d.Domains {
			if err := (&validate.V{}).Hostname(host); err != nil {
				return err
			}
			transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}
			client := &http.Client{Timeout: 5 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			err := probeGet(ctx, client, httpEndpoint+"/", host, d.Maintenance, false)
			if err == nil {
				err = probeGet(ctx, client, httpEndpoint+"/.well-known/acme-challenge/novapanel-probe", host, false, true)
			}
			client.CloseIdleConnections()
			if err != nil {
				return err
			}
			if secure != nil && d.TLS.covers(host) {
				cfg := config.Clone()
				cfg.ServerName = host
				if d.TLS.leaf != nil {
					priorVerify := cfg.VerifyConnection
					// Trust only the validated installed leaf, never an arbitrary private CA.
					cfg.InsecureSkipVerify = true
					cfg.VerifyConnection = func(cs tls.ConnectionState) error {
						if len(cs.PeerCertificates) == 0 || !bytes.Equal(cs.PeerCertificates[0].Raw, d.TLS.leaf.Raw) {
							return rpcerr.Errf("agent_error", "Unexpected served TLS certificate")
						}
						leaf := cs.PeerCertificates[0]
						now := time.Now()
						if cfg.Time != nil {
							now = cfg.Time()
						}
						if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) || leaf.VerifyHostname(host) != nil || cs.ServerName != host {
							return rpcerr.Errf("agent_error", "Invalid served TLS certificate")
						}
						if priorVerify != nil {
							return priorVerify(cs)
						}
						return nil
					}
				}
				// URL carries the hostname for certificate validation; dial ignores DNS.
				transport = &http.Transport{Proxy: nil, TLSClientConfig: cfg, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", secure.Host)
				}}
				client.Transport = transport
				target := *secure
				target.Host = net.JoinHostPort(host, secure.Port())
				target.Path = "/"
				err = probeGet(ctx, client, target.String(), host, d.Maintenance, false)
				client.CloseIdleConnections()
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
}
func probeEndpoint(endpoint, scheme string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != scheme || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Port() == "" {
		return nil, rpcerr.Errf("invalid_argument", "Invalid loopback probe endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, rpcerr.Errf("invalid_argument", "Probe must use loopback")
	}
	return u, nil
}
func probeGet(ctx context.Context, client *http.Client, target, host string, maintenance, acme bool) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Host = host
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 500 && !(maintenance && res.StatusCode == 503) || acme && res.StatusCode != 200 && res.StatusCode != 404 {
		return rpcerr.Errf("agent_error", "Nginx health probe failed")
	}
	return nil
}

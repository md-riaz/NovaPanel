package acme

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"novapanel/internal/secret"
	paneltls "novapanel/internal/tls"
)

// Order is passed before ANY network I/O. BeforeOrder must durably reserve the
// registered-domain, duplicate-set and auth-failure budgets or return an error.
type Order struct {
	Operation   string
	Environment Environment
	Domains     []string
}

type Config struct {
	Environment          Environment
	Directory            string // Read from agent Cfg; empty uses the staging directory only.
	Email                string
	Sealer               *secret.Sealer
	SaveAccount          func(AccountRecord) error // Must commit registration before an order.
	BeforeOrder          func(Order) error         // Required; no permissive default.
	TermsOfServiceAgreed bool
	HTTPClient           *http.Client  // Optional trusted transport/roots, never a web-tier input.
	Timeout              time.Duration // HTTP/request and lego polling bound; callbacks/sleeps may delay return.
}

// Client is execution-local. Do not log it, copy it, or share it across accounts.
type Client struct {
	mu      *sync.Mutex
	cfg     Config
	account *account
}

func (c Client) String() string   { return "ACME client (redacted)" }
func (c Client) GoString() string { return c.String() }

// New validates and opens a sealed record, without network I/O. Account creation
// and registration persistence are explicit; New never creates missing accounts.
func New(cfg Config, record AccountRecord) (*Client, error) {
	if cfg.Environment == "" {
		cfg.Environment = Staging
	}
	if cfg.Directory == "" && cfg.Environment == Staging {
		cfg.Directory = lego.LEDirectoryStaging
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if !validDirectory(cfg.Directory) || !validEmail(cfg.Email, cfg.Environment) || cfg.Timeout <= 0 || cfg.Timeout > 10*time.Minute || cfg.SaveAccount == nil || cfg.BeforeOrder == nil {
		return nil, errAccount
	}
	a, e := openAccount(cfg, record)
	if e != nil {
		return nil, e
	}
	return &Client{mu: &sync.Mutex{}, cfg: cfg, account: a}, nil
}

var ErrCA = errors.New("ACME operation failed; inspect private coordinator diagnostics")

// Issue obtains one bundled SAN certificate for the complete persisted site domain
// set, primary first. It does not install, reload, enqueue, audit, or mark coverage.
func (c *Client) Issue(ctx context.Context, domains []string, webroot string) (*paneltls.Certificate, error) {
	return c.obtain(ctx, domains, webroot, nil)
}

// Renew places a fresh all-site SAN order with an RFC 9773 replacement identifier.
// The coordinator decides when renewal is due and supplies the previously installed
// fullchain. Domain expansion is supported; old private keys are never reused.
func (c *Client) Renew(ctx context.Context, domains []string, webroot string, previousFullchain []byte) (*paneltls.Certificate, error) {
	if len(previousFullchain) == 0 || len(previousFullchain) > paneltls.MaxCertificateBytes {
		return nil, errors.New("invalid prior ACME certificate")
	}
	return c.obtain(ctx, domains, webroot, previousFullchain)
}

func (c *Client) obtain(ctx context.Context, domains []string, root string, previous []byte) (*paneltls.Certificate, error) {
	if err := checkDomains(domains); err != nil {
		return nil, err
	}
	domains = append([]string(nil), domains...)
	replacement := ""
	operation := "issue"
	if previous != nil {
		certs, e := certcrypto.ParsePEMBundle(previous)
		if e != nil || len(certs) == 0 || certs[0].IsCA {
			return nil, errors.New("invalid prior ACME certificate")
		}
		replacement, e = certificate.MakeARICertID(certs[0])
		if e != nil {
			return nil, errors.New("prior certificate has no ACME replacement identity")
		}
		operation = "renew"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider, err := NewWebroot(root, domains)
	if err != nil {
		return nil, err
	}
	defer provider.Close()
	if c.account.data.Registration == nil && !c.cfg.TermsOfServiceAgreed {
		return nil, errors.New("ACME terms agreement required")
	}
	if err = c.cfg.BeforeOrder(Order{Operation: operation, Environment: c.cfg.Environment, Domains: append([]string(nil), domains...)}); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	cfg := lego.NewConfig(c.account)
	cfg.CADirURL = c.cfg.Directory
	cfg.Certificate.KeyType = certcrypto.EC256
	cfg.Certificate.Timeout = c.cfg.Timeout
	cfg.UserAgent = "NovaPanel"
	base := http.DefaultTransport
	if c.cfg.HTTPClient != nil && c.cfg.HTTPClient.Transport != nil {
		base = c.cfg.HTTPClient.Transport
	}
	cfg.HTTPClient = &http.Client{Transport: boundedTransport{base: base, ctx: ctx, directory: c.cfg.Directory}, Timeout: c.cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("ACME redirects disabled") }}
	client, err := lego.NewClient(cfg) // Fetches directory; policy reservation has already committed.
	if err != nil {
		return nil, operationError(ctx)
	}
	if err = client.Challenge.SetHTTP01Provider(provider); err != nil {
		return nil, ErrCA
	}
	if c.account.data.Registration == nil {
		reg, e := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: c.cfg.TermsOfServiceAgreed})
		if e != nil {
			return nil, operationError(ctx)
		}
		c.account.data.Registration = reg
	}
	// Save even on a retry after a prior save failed. Never order with unsaved registration.
	record, err := sealAccount(c.cfg.Sealer, c.account.data)
	if err != nil {
		return nil, err
	}
	if err = c.cfg.SaveAccount(record); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	resource, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: domains, Bundle: true, ReplacesCertID: replacement})
	if err != nil {
		return nil, operationError(ctx)
	}
	if resource == nil {
		return nil, ErrCA
	}
	defer clear(resource.PrivateKey)
	result, err := paneltls.Validate(resource.Certificate, resource.PrivateKey, domains, time.Now())
	if err != nil {
		return nil, ErrCA
	}
	if err = provider.Close(); err != nil {
		return nil, err
	}
	return result, nil
}
func operationError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrCA // Upstream errors can contain challenge/key material; never pass them to RPC.
}

type boundedTransport struct {
	base      http.RoundTripper
	ctx       context.Context
	directory string
}

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	d, _ := url.Parse(t.directory)
	if r.URL.Scheme != d.Scheme || r.URL.Host != d.Host || r.URL.User != nil {
		return nil, ErrCA
	}
	res, e := t.base.RoundTrip(r.Clone(t.ctx))
	if e != nil {
		return nil, e
	}
	if res.ContentLength > 1<<20 {
		res.Body.Close()
		return nil, ErrCA
	}
	res.Body = &boundedBody{Reader: io.LimitReader(res.Body, 1<<20), Closer: res.Body}
	return res, nil
}

type boundedBody struct {
	io.Reader
	io.Closer
}

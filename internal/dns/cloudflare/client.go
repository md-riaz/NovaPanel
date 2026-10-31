// Package cloudflare implements fixed-origin Cloudflare API requests.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"novapanel/internal/dns"
	"novapanel/internal/rpcerr"
	"novapanel/internal/validate"
	"regexp"
	"strings"
	"time"
)

var identifier = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Client struct {
	// A closure keeps fmt's invalid-verb fallback from traversing the raw token.
	token func() string
	zones map[string]string
	http  *http.Client
}

func (c Client) String() string   { return "Cloudflare client (redacted)" }
func (c Client) GoString() string { return c.String() }

// Format prevents unsupported verbs from falling back to credential-bearing fields.
func (c Client) Format(state fmt.State, verb rune) { _, _ = io.WriteString(state, c.String()) }

func New(token string, zones map[string]string, transport http.RoundTripper) (*Client, error) {
	if token == "" || len(zones) == 0 {
		return nil, rpcerr.Errf("invalid_argument", "Invalid Cloudflare configuration")
	}
	for _, b := range []byte(token) {
		if b < 33 || b > 126 {
			return nil, rpcerr.Errf("invalid_argument", "Invalid Cloudflare configuration")
		}
	}
	bindings := make(map[string]string, len(zones))
	for zone, id := range zones {
		if (&validate.V{}).Hostname(zone) != nil || !identifier.MatchString(id) {
			return nil, rpcerr.Errf("invalid_argument", "Invalid Cloudflare zone")
		}
		lower := strings.ToLower(zone)
		if _, exists := bindings[lower]; exists {
			return nil, rpcerr.Errf("invalid_argument", "Duplicate Cloudflare zone")
		}
		bindings[lower] = id
	}
	return &Client{token: func() string { return token }, zones: bindings, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func providerError() error { return rpcerr.Errf("agent_error", "Invalid Cloudflare response") }
func (c *Client) zonePath(zone string) (string, error) {
	if (&validate.V{}).Hostname(zone) != nil {
		return "", rpcerr.Errf("invalid_argument", "Invalid zone")
	}
	id, ok := c.zones[strings.ToLower(zone)]
	if !ok {
		return "", rpcerr.Errf("forbidden", "Cloudflare zone is not authorized")
	}
	return "/zones/" + id, nil
}

// request never returns raw provider or transport errors. Unknown envelope fields
// are ignored, but the success flag and typed result must validate independently.
func (c *Client) request(ctx context.Context, method, path string, body any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return providerError()
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4"+path, bytes.NewReader(payload))
	if err != nil {
		return providerError()
	}
	req.Header.Set("Authorization", "Bearer "+c.token())
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return providerError()
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerError()
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return providerError()
	}
	if json.Unmarshal(raw, out) != nil {
		return providerError()
	}
	return nil
}

type wireRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
	Proxied  bool   `json:"proxied"`
}

func wire(r dns.Record) wireRecord {
	w := wireRecord{Type: r.Type, Name: strings.ToLower(r.Name), Content: r.Value, TTL: r.TTL, Proxied: r.Proxied}
	if r.Type == "MX" {
		w.Priority = &r.Priority
	}
	return w
}
func (w wireRecord) record(zone string) (dns.Record, error) {
	r := dns.Record{ID: w.ID, Type: w.Type, Name: strings.ToLower(w.Name), Value: w.Content, TTL: w.TTL, Proxied: w.Proxied}
	if w.Priority != nil {
		r.Priority = *w.Priority
	}
	if !identifier.MatchString(r.ID) || r.Validate(zone) != nil {
		return dns.Record{}, providerError()
	}
	return r, nil
}

// Create is not a durable ensure-state worker. Never retry ambiguous creation.
func (c *Client) Create(ctx context.Context, zone string, r dns.Record) (dns.Record, error) {
	if r.ID != "" {
		return dns.Record{}, rpcerr.Errf("invalid_argument", "Create requires an empty record ID")
	}
	if err := r.Validate(zone); err != nil {
		return dns.Record{}, err
	}
	path, err := c.zonePath(zone)
	if err != nil {
		return dns.Record{}, err
	}
	var env struct {
		Success bool       `json:"success"`
		Result  wireRecord `json:"result"`
	}
	if err = c.request(ctx, "POST", path+"/dns_records", wire(r), &env); err != nil {
		return dns.Record{}, err
	}
	if !env.Success {
		return dns.Record{}, providerError()
	}
	result, err := env.Result.record(zone)
	if err != nil || result.Type != r.Type || !strings.EqualFold(result.Name, r.Name) {
		return dns.Record{}, providerError()
	}
	return result, nil
}

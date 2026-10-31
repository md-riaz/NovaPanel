// Package cloudflare implements fixed-origin Cloudflare API requests.
// It has no persistence and is not wired into the agent or public RPC.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
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

// Client holds only an execution-lifetime token opened by its future agent owner.
// New copies authorized zone bindings; there is no caller-controlled endpoint.
type Client struct {
	token string
	zones map[string]string
	http  *http.Client
}

func New(token string, zones map[string]string, transport http.RoundTripper) (*Client, error) {
	if token == "" || strings.ContainsAny(token, "\r\n\x00 \t") || len(zones) == 0 {
		return nil, rpcerr.Errf("invalid_argument", "Invalid Cloudflare configuration")
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
	return &Client{token: token, zones: bindings, http: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Create validates before any HTTP, then returns the provider-assigned ID.
// It is a request adapter, not the F7 durable ensure-state worker; do not retry
// creation automatically after an ambiguous response or expose it as a verb.
func (c *Client) Create(ctx context.Context, zone string, r dns.Record) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if r.ID != "" {
		return "", rpcerr.Errf("invalid_argument", "Create requires an empty record ID")
	}
	if err := r.Validate(zone); err != nil {
		return "", err
	}
	zid, ok := c.zones[strings.ToLower(zone)]
	if !ok {
		return "", rpcerr.Errf("forbidden", "Cloudflare zone is not authorized")
	}
	body := struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Content  string `json:"content"`
		TTL      int    `json:"ttl"`
		Priority *int   `json:"priority,omitempty"`
		Proxied  bool   `json:"proxied"`
	}{Type: r.Type, Name: r.Name, Content: r.Value, TTL: r.TTL, Proxied: r.Proxied}
	if r.Type == "MX" {
		body.Priority = &r.Priority
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", rpcerr.Errf("agent_error", "Cannot encode DNS record")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudflare.com/client/v4/zones/"+zid+"/dns_records", bytes.NewReader(payload))
	if err != nil {
		return "", rpcerr.Errf("agent_error", "Cannot prepare DNS request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", rpcerr.Errf("agent_error", "Cloudflare request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", rpcerr.Errf("agent_error", "Cloudflare rejected DNS request")
	}
	const maxResponse = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return "", rpcerr.Errf("agent_error", "Invalid Cloudflare response")
	}
	var envelope struct {
		Success bool `json:"success"`
		Result  struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil || !envelope.Success || !identifier.MatchString(envelope.Result.ID) {
		return "", rpcerr.Errf("agent_error", "Invalid Cloudflare response")
	}
	return envelope.Result.ID, nil
}

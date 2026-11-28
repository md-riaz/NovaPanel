package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"novapanel/internal/dns"
)

var errProvider = errors.New("cloudflare DNS request failed")
var errDeferred = errors.New("not implemented in this slice")

// Provider uses zone IDs resolved by the integration probe.
type Provider struct {
	token  string
	zones  map[string]string
	client *http.Client
}

func New(token string, zones map[string]string) *Provider {
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Provider{token: token, zones: zones, client: &client}
}

type cfRecord struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Priority *int   `json:"priority,omitempty"`
	Proxied  *bool  `json:"proxied,omitempty"`
	Comment  string `json:"comment"`
}

func toCF(r dns.Record) cfRecord {
	v := cfRecord{Type: r.Type, Name: r.Name, Content: r.Value, TTL: r.TTL, Comment: r.Comment}
	if r.Type == "MX" {
		v.Priority = &r.Priority
	}
	if r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME" {
		v.Proxied = &r.Proxied
	}
	return v
}
func fromCF(v cfRecord) dns.Record {
	r := dns.Record{ID: v.ID, Type: v.Type, Name: v.Name, Value: v.Content, TTL: v.TTL, Comment: v.Comment}
	if v.Priority != nil {
		r.Priority = *v.Priority
	}
	if v.Proxied != nil {
		r.Proxied = *v.Proxied
	}
	return r
}

func (p *Provider) request(ctx context.Context, method, path string, input any, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return errProvider
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4"+path, body)
	if err != nil {
		return errProvider
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return errProvider
	}
	defer res.Body.Close()
	var envelope struct {
		Success    bool            `json:"success"`
		Result     json.RawMessage `json:"result"`
		ResultInfo struct {
			TotalPages int `json:"total_pages"`
		} `json:"result_info"`
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&envelope) != nil || !envelope.Success {
		return errProvider
	}
	if output != nil {
		if err := json.Unmarshal(envelope.Result, output); err != nil {
			return errProvider
		}
	}
	return nil
}

func (p *Provider) path(zone string) (string, error) {
	zoneID := p.zones[zone]
	if zoneID == "" {
		return "", errProvider
	}
	return "/zones/" + url.PathEscape(zoneID) + "/dns_records", nil
}

func (p *Provider) List(ctx context.Context, zone string) ([]dns.Record, error) {
	path, err := p.path(zone)
	if err != nil {
		return nil, err
	}
	var out []dns.Record
	for page := 1; ; page++ {
		var records []cfRecord
		// A short page terminates the complete listing; an extra empty page is harmless.
		if err := p.request(ctx, http.MethodGet, fmt.Sprintf("%s?per_page=100&page=%d", path, page), nil, &records); err != nil {
			return nil, err
		}
		for _, r := range records {
			out = append(out, fromCF(r))
		}
		if len(records) < 100 {
			return out, nil
		}
	}
}
func (p *Provider) Create(ctx context.Context, zone string, r dns.Record) (string, error) {
	path, err := p.path(zone)
	if err != nil {
		return "", err
	}
	var result cfRecord
	if err = p.request(ctx, http.MethodPost, path, toCF(r), &result); err != nil {
		return "", err
	}
	if result.ID == "" {
		return "", errProvider
	}
	return result.ID, nil
}
func (p *Provider) Update(ctx context.Context, zone string, r dns.Record) error {
	path, err := p.path(zone)
	if err != nil {
		return err
	}
	if r.ID == "" {
		return errProvider
	}
	return p.request(ctx, http.MethodPut, path+"/"+url.PathEscape(r.ID), toCF(r), nil)
}
func (p *Provider) Delete(ctx context.Context, zone string, r dns.Record) error {
	path, err := p.path(zone)
	if err != nil {
		return err
	}
	if r.ID == "" {
		return errProvider
	}
	return p.request(ctx, http.MethodDelete, path+"/"+url.PathEscape(r.ID), nil, nil)
}
func (*Provider) Verify(context.Context, string) (bool, error)     { return false, errDeferred }
func (*Provider) Purge(context.Context, string, []string) error    { return errDeferred }
func (*Provider) PresentTXT(context.Context, string, string) error { return errDeferred }
func (*Provider) CleanupTXT(context.Context, string, string) error { return errDeferred }

var _ dns.Provider = (*Provider)(nil)

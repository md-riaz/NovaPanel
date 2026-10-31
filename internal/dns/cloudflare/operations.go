package cloudflare

import (
	"context"
	"fmt"
	"novapanel/internal/dns"
	"novapanel/internal/rpcerr"
)

func (c *Client) Update(ctx context.Context, zone string, r dns.Record) error {
	if !identifier.MatchString(r.ID) {
		return rpcerr.Errf("invalid_argument", "Invalid record ID")
	}
	if err := r.Validate(zone); err != nil {
		return err
	}
	path, err := c.zonePath(zone)
	if err != nil {
		return err
	}
	var env struct {
		Success bool       `json:"success"`
		Result  wireRecord `json:"result"`
	}
	if err = c.request(ctx, "PUT", path+"/dns_records/"+r.ID, wire(r), &env); err != nil {
		return err
	}
	result, err := env.Result.record(zone)
	if !env.Success || err != nil || result.ID != r.ID {
		return providerError()
	}
	return nil
}
func (c *Client) Delete(ctx context.Context, zone, id string) error {
	if !identifier.MatchString(id) {
		return rpcerr.Errf("invalid_argument", "Invalid record ID")
	}
	path, err := c.zonePath(zone)
	if err != nil {
		return err
	}
	var env struct {
		Success bool `json:"success"`
		Result  struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err = c.request(ctx, "DELETE", path+"/dns_records/"+id, nil, &env); err != nil {
		return err
	}
	if !env.Success || env.Result.ID != id {
		return providerError()
	}
	return nil
}

// List either returns the complete bounded list or an error, never partial success.
func (c *Client) List(ctx context.Context, zone string) ([]dns.Record, error) {
	path, err := c.zonePath(zone)
	if err != nil {
		return nil, err
	}
	result := []dns.Record{}
	seen := map[string]bool{}
	total := 0
	for page := 1; page <= 10; page++ {
		var env struct {
			Success bool         `json:"success"`
			Result  []wireRecord `json:"result"`
			Info    struct {
				Page  int `json:"page"`
				Total int `json:"total_pages"`
			} `json:"result_info"`
		}
		if err = c.request(ctx, "GET", fmt.Sprintf("%s/dns_records?page=%d&per_page=100", path, page), nil, &env); err != nil {
			return nil, err
		}
		if !env.Success || env.Result == nil || len(env.Result) > 100 || env.Info.Page != page || env.Info.Total < 1 || env.Info.Total > 10 || (total != 0 && total != env.Info.Total) {
			return nil, providerError()
		}
		total = env.Info.Total
		for _, w := range env.Result {
			r, e := w.record(zone)
			if e != nil || seen[r.ID] {
				return nil, providerError()
			}
			seen[r.ID] = true
			result = append(result, r)
		}
		if page == total {
			return result, nil
		}
	}
	return nil, providerError()
}

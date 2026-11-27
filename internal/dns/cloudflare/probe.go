package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

// Probe checks zone visibility without changing provider state.
func Probe(ctx context.Context, token string, zones []string) error {
	return probe(ctx, http.DefaultClient, token, zones)
}

func probe(ctx context.Context, client *http.Client, token string, zones []string) error {
	for _, zone := range zones {
		u := "https://api.cloudflare.com/client/v4/zones?name=" + url.QueryEscape(zone)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return errors.New("zone access check failed")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		// Do not forward credentials to redirects.
		c := *client
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, err := c.Do(req)
		if err != nil {
			return errors.New("zone access check failed")
		}
		var body struct {
			Success bool `json:"success"`
			Result  []struct {
				Name string `json:"name"`
			} `json:"result"`
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body)
		res.Body.Close()
		if err != nil || res.StatusCode != http.StatusOK || !body.Success || len(body.Result) != 1 || body.Result[0].Name != zone {
			return errors.New("zone access check failed")
		}
	}
	return nil
}

package nginx

import (
	"context"
	"net"
	"net/http"
	"novapanel/internal/rpcerr"
	"time"
)

// HTTPProbe uses a fixed loopback endpoint, never DNS supplied by the site.
func HTTPProbe(endpoint string) func(context.Context, Desired) error {
	return func(ctx context.Context, d Desired) error {
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer client.CloseIdleConnections()
		for _, host := range d.Domains {
			for _, path := range []string{"/", "/.well-known/acme-challenge/novapanel-probe"} {
				req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
				if e != nil {
					return e
				}
				req.Host = host
				res, e := client.Do(req)
				if e != nil {
					return e
				}
				res.Body.Close()
				if res.StatusCode >= 500 && !(d.Maintenance && path == "/" && res.StatusCode == 503) || path != "/" && res.StatusCode != 200 && res.StatusCode != 404 {
					return rpcerr.Errf("agent_error", "Nginx health probe failed")
				}
			}
		}
		return nil
	}
}

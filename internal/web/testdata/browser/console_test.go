package web_test

import (
	"context"
	"net/http/httptest"
	"novapanel/internal/rpc"
	"novapanel/internal/web"
	"os"
	"os/exec"
	"sync"
	"testing"
)

// Browser contract acceptance only. RPC fixtures do not provision sites or issue certificates.
type consoleRPC struct {
	mu       sync.Mutex
	created  bool
	polls    int
	uploaded bool
	promoted bool
}

func (c *consoleRPC) Close() error { return nil }
func (c *consoleRPC) Call(_ context.Context, verb string, args any, _ string) (rpc.Reply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data := map[string]any{}
	switch verb {
	case "session.whoami":
		data = map[string]any{"role": "admin", "user": "fixture", "csrf_token": "expected"}
	case "site.list":
		rows := []any{}
		if c.created {
			rows = append(rows, map[string]any{"id": "SITE", "name": "demo", "type": "static", "status": "active", "primary_domain": "example.test"})
		}
		data["sites"] = rows
	case "site.get":
		data = map[string]any{"site": map[string]any{"name": "demo", "type": "static", "status": "active"}, "redirects": []any{map[string]any{"id": "REDIRECT", "source": "/old", "target": "/new", "code": 301}}, "domains": []any{map[string]any{"id": "DOMAIN", "fqdn": "example.test", "is_primary": true, "cert_state": "none"}}}
	case "site.update":
		c.promoted = args.(map[string]any)["primary_domain_id"] == "DOMAIN"
		data["job"] = map[string]any{"id": "JOB", "verb": verb, "state": "queued"}
	case "ssl.status":
		data = map[string]any{"source": "none", "ssl_auto": false, "force_https": false, "domains": []any{map[string]any{"fqdn": "example.test", "cert_state": "none"}}}
	case "ssl.renew":
		return rpc.Reply{Code: "unknown_verb", Error: "not implemented"}, nil
	case "ssl.upload":
		a := args.(map[string]any)
		c.uploaded = a["key_pem"] == "PRIVATE" && a["cert_pem"] == "CERT"
		return rpc.Reply{Code: "forbidden", Data: map[string]any{"reauth_required": true}}, nil
	case "job.status":
		c.polls++
		data = map[string]any{"id": "JOB", "verb": "site.create", "state": "done", "result": map[string]any{"site_id": "SITE"}}
	default:
		if verb == "site.create" {
			c.created = true
		}
		data["job"] = map[string]any{"id": "JOB", "verb": verb, "state": "queued"}
	}
	return rpc.Reply{OK: true, Data: data}, nil
}
func TestBrowserConsoleContract(t *testing.T) {
	node := os.Getenv("NOVAPANEL_BROWSER_NODE")
	if node == "" {
		t.Skip("set NOVAPANEL_BROWSER_NODE for isolated Chromium contract acceptance")
	}
	fixture := &consoleRPC{}
	ts := httptest.NewUnstartedServer(nil)
	defer ts.Close()
	h, err := web.New(web.Options{Origin: "https://" + ts.Listener.Addr().String(), RateLimitBurst: 100, Dial: func() (web.RPCClient, error) { return fixture, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = h
	ts.StartTLS()
	cmd := exec.Command(node, "../console_browser.cjs", ts.URL)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !fixture.created || fixture.polls == 0 || !fixture.uploaded || !fixture.promoted {
		t.Fatalf("fixture create=%v polls=%d upload=%v", fixture.created, fixture.polls, fixture.uploaded)
	}
}

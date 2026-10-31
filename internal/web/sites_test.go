package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"reflect"
	"strings"
	"testing"
)

func TestConsoleMutationsCanonicalArgs(t *testing.T) {
	for _, tc := range []struct {
		path, body, verb string
		want             map[string]any
	}{
		{"/sites/create", "name=demo&type=static&primary_domain=example.test&evil=ignore", "site.create", map[string]any{"name": "demo", "type": "static", "primary_domain": "example.test"}},
		{"/sites/SITE/update", "primary_domain_id=DOMAIN", "site.update", map[string]any{"site_id": "SITE", "primary_domain_id": "DOMAIN"}},
		{"/sites/SITE/update", "maintenance=false", "site.update", map[string]any{"site_id": "SITE", "maintenance": false}},
		{"/sites/SITE/delete", "purge_files=true", "site.delete", map[string]any{"site_id": "SITE", "purge_files": true}},
		{"/sites/SITE/domains/add", "fqdn=www.example.test", "domain.add", map[string]any{"site_id": "SITE", "fqdn": "www.example.test"}},
		{"/sites/SITE/domains/remove", "domain_id=DOMAIN", "domain.remove", map[string]any{"domain_id": "DOMAIN"}},
		{"/sites/SITE/redirects/add", "source=%2Fold&target=%2Fnew&code=301", "redirect.add", map[string]any{"site_id": "SITE", "source": "/old", "target": "/new", "code": 301}},
		{"/sites/SITE/redirects/remove", "redirect_id=REDIRECT", "redirect.remove", map[string]any{"redirect_id": "REDIRECT"}},
		{"/sites/SITE/ssl/issue", "staging=true", "ssl.issue", map[string]any{"site_id": "SITE", "staging": true}},
		{"/sites/SITE/ssl/renew", "", "ssl.renew", map[string]any{"site_id": "SITE"}},
		{"/sites/SITE/ssl/upload", "cert_pem=CERT&key_pem=PRIVATE", "ssl.upload", map[string]any{"site_id": "SITE", "cert_pem": "CERT", "key_pem": "PRIVATE"}},
		{"/sites/SITE/ssl/auto", "enabled=false", "ssl.auto_set", map[string]any{"site_id": "SITE", "enabled": false}},
		{"/sites/SITE/update", "force_https=true", "site.update", map[string]any{"site_id": "SITE", "force_https": true}},
	} {
		t.Run(tc.verb+tc.body, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(v string) (rpc.Reply, error) {
				if v == "session.whoami" {
					return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
				}
				if v == "ssl.status" {
					return rpc.Reply{OK: true, Data: map[string]any{"source": "none", "ssl_auto": false, "force_https": false, "domains": []any{}}}, nil
				}
				if v == "ssl.auto_set" {
					return rpc.Reply{OK: true, Data: map[string]any{}}, nil
				}
				return rpc.Reply{OK: true, Data: map[string]any{"job": map[string]any{"id": "JOB", "state": "queued", "verb": v}}}, nil
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("POST", tc.path, tc.body))
			if w.Code != 202 && !(tc.verb == "ssl.auto_set" && w.Code == 200) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			wantCalls := 2
			if tc.verb == "ssl.auto_set" {
				wantCalls = 3
				if !strings.Contains(w.Body.String(), "Auto-renew: false") || f.rpcCalls[2].verb != "ssl.status" {
					t.Fatalf("persisted preference not refreshed: %s %+v", w.Body.String(), f.rpcCalls)
				}
			}
			if len(f.rpcCalls) != wantCalls || f.rpcCalls[1].verb != tc.verb || !reflect.DeepEqual(f.rpcCalls[1].args, tc.want) {
				t.Fatalf("calls %+v want %+v", f.rpcCalls, tc.want)
			}
			if strings.Contains(w.Body.String(), "PRIVATE") || strings.Contains(w.Body.String(), "CERT") {
				t.Fatal("upload echoed")
			}
		})
	}
}

func TestConsoleErrorCodesAndSecurity(t *testing.T) {
	for _, code := range []string{"unknown_verb", "unsupported", "precondition_failed", "locked", "rate_limited", "not_found", "unauthenticated"} {
		t.Run(code, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(v string) (rpc.Reply, error) {
				if v == "session.whoami" {
					return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
				}
				return rpc.Reply{OK: false, Code: code, Error: "agent message must not echo private inputs"}, nil
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("POST", "/sites/SITE/ssl/renew", ""))
			if w.Code < 400 || !strings.Contains(w.Body.String(), code) || strings.Contains(w.Body.String(), "agent message") {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	f := identityRPC()
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("POST", "/sites/SITE/delete", ""))
	if w.Code != 403 || len(f.rpcCalls) != 1 {
		t.Fatalf("reader mutation %d %+v", w.Code, f.rpcCalls)
	}
}

func TestConsoleDetailSSLAndJobPages(t *testing.T) {
	for _, tc := range []struct {
		path, verb, marker string
		data               map[string]any
	}{
		{"/sites/SITE", "site.get", "Domains", map[string]any{"site": map[string]any{"name": "demo", "status": "active", "type": "static"}, "domains": []any{map[string]any{"id": "DOMAIN", "fqdn": "example.test", "is_primary": true, "cert_state": "none"}}}},
		{"/sites/SITE/ssl", "ssl.status", "Force HTTPS", map[string]any{"source": "none", "force_https": false, "domains": []any{map[string]any{"fqdn": "example.test", "cert_state": "none"}}}},
		{"/jobs/JOB", "job.status", "queued", map[string]any{"id": "JOB", "state": "queued", "verb": "site.create", "step": "waiting", "progress": 0}},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(v string) (rpc.Reply, error) {
				if v == tc.verb {
					return rpc.Reply{OK: true, Data: tc.data}, nil
				}
				return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("GET", tc.path, ""))
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.marker) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if len(f.rpcCalls) != 2 || f.rpcCalls[1].verb != tc.verb {
				t.Fatalf("%+v", f.rpcCalls)
			}
		})
	}
}

func TestSitesListUsesAgentAndEscapesNames(t *testing.T) {
	f := identityRPC()
	f.reply = func(v string) (rpc.Reply, error) {
		if v == "site.list" {
			return rpc.Reply{OK: true, Data: map[string]any{"sites": []any{map[string]any{"id": "01SITE", "name": "<script>alert(1)</script>", "primary_domain": "example.test", "status": "active", "type": "static"}}}}, nil
		}
		return rpc.Reply{OK: true, Data: map[string]any{"user": "alice", "role": "admin", "csrf_token": "expected"}}, nil
	}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("GET", "/sites", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "example.test") || strings.Contains(w.Body.String(), "<script>alert") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(f.rpcCalls) != 2 || f.rpcCalls[1].verb != "site.list" {
		t.Fatalf("calls %+v", f.rpcCalls)
	}
}

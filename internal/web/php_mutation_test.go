package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

func TestPHPJobFeedback(t *testing.T) {
	for _, state := range []string{"queued", "running", "failed", "done"} {
		t.Run(state, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(verb string) (rpc.Reply, error) {
				switch verb {
				case "session.whoami":
					return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
				case "job.status":
					return rpc.Reply{OK: true, Data: map[string]any{"id": "JOB", "verb": "php.set", "object_key": "site:SITE", "state": state, "error": "<bad>", "code": "agent_error"}}, nil
				case "site.get":
					return rpc.Reply{OK: true, Data: map[string]any{"site": map[string]any{"type": "php", "name": "demo", "php_version": "8.3"}, "php_limits": map[string]any{"memory_mb": 768, "upload_mb": 64, "max_exec_sec": 60}}}, nil
				default:
					t.Fatalf("unexpected %s", verb)
					return rpc.Reply{}, nil
				}
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("GET", "/sites/SITE/php/jobs/JOB", ""))
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			switch state {
			case "queued", "running":
				if !strings.Contains(body, `data-job-poll="/sites/SITE/php/jobs/JOB"`) {
					t.Fatal(body)
				}
			case "failed":
				if !strings.Contains(body, "&lt;bad&gt;") || strings.Contains(body, "<bad>") {
					t.Fatal(body)
				}
			case "done":
				if !strings.Contains(body, "Memory: 768 MiB") || len(f.rpcCalls) != 3 || f.rpcCalls[2].verb != "site.get" {
					t.Fatal(body, f.rpcCalls)
				}
			}
		})
	}
}

func TestPHPPostSecurity(t *testing.T) {
	body := "version=8.3&memory_mb=256&upload_mb=64&max_exec_sec=60"
	f := identityRPC()
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("POST", "/sites/SITE/php", body))
	if w.Code != 403 || len(f.rpcCalls) != 1 {
		t.Fatal(w.Code, f.rpcCalls)
	}
	f = identityRPC()
	f.reply = func(v string) (rpc.Reply, error) {
		return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
	}
	r := request("POST", "/sites/SITE/php", body)
	r.Header.Del("X-CSRF-Token")
	w = httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, r)
	if w.Code != 403 || len(f.rpcCalls) != 1 {
		t.Fatal(w.Code, f.rpcCalls)
	}
}

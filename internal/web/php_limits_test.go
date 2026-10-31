package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

func TestPHPPersistedLimitsView(t *testing.T) {
	for _, role := range []string{"read_only", "admin", "owner"} {
		t.Run(role, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(verb string) (rpc.Reply, error) {
				switch verb {
				case "session.whoami":
					return rpc.Reply{OK: true, Data: map[string]any{"role": role, "csrf_token": "expected"}}, nil
				case "site.get":
					return rpc.Reply{OK: true, Data: map[string]any{"site": map[string]any{"name": "demo", "type": "php", "status": "active", "php_version": "8.3"}, "php_limits": map[string]any{"memory_mb": 512, "upload_mb": 100, "max_exec_sec": 120}, "domains": []any{}, "redirects": []any{}}}, nil
				default:
					t.Fatalf("unexpected RPC %s", verb)
					return rpc.Reply{}, nil
				}
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("GET", "/sites/SITE", ""))
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			for _, text := range []string{"PHP desired settings", "Version: 8.3", "Memory: 512 MiB", "Upload: 100 MiB", "Execution time: 120 seconds", "PHP settings cannot be changed until runtime publication is available."} {
				if !strings.Contains(w.Body.String(), text) {
					t.Errorf("missing %q", text)
				}
			}
			if strings.Contains(w.Body.String(), `action="/sites/SITE/php"`) {
				t.Fatal("unavailable PHP mutation exposed")
			}
			if len(f.rpcCalls) != 2 {
				t.Fatal(f.rpcCalls)
			}
		})
	}
}

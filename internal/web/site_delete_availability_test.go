package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

func TestSiteDeleteAvailability(t *testing.T) {
	for _, kind := range []string{"static", "php", "wordpress"} {
		t.Run(kind, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(v string) (rpc.Reply, error) {
				if v == "session.whoami" {
					return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
				}
				return rpc.Reply{OK: true, Data: map[string]any{"site": map[string]any{"name": "demo", "type": kind}, "domains": []any{}, "redirects": []any{}}}, nil
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("GET", "/sites/SITE", ""))
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			hasForm := strings.Contains(w.Body.String(), `action="/sites/SITE/delete"`)
			if hasForm != (kind == "static") {
				t.Fatalf("type=%s delete form=%v", kind, hasForm)
			}
			if kind != "static" && !strings.Contains(w.Body.String(), "Deletion of PHP and WordPress sites is not yet available.") {
				t.Fatal("missing unavailable notice")
			}
			if kind == "static" && !strings.Contains(w.Body.String(), "Sites with attached databases cannot yet be deleted.") {
				t.Fatal("missing attached database warning")
			}
		})
	}
}

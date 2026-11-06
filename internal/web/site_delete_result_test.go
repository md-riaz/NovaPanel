package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

func TestSiteDeleteOtherPreconditions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result any
	}{
		{"tls", nil},
		{"docroot", map[string]any{}},
		{"malformed result", "unexpected"},
		{"empty", map[string]any{"attached_database_ids": []any{}}},
		{"invalid", map[string]any{"attached_database_ids": []any{`<script>alert(1)</script>`}}},
		{"wrong type", map[string]any{"attached_database_ids": "not validated"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := identityRPC()
			f.reply = func(v string) (rpc.Reply, error) {
				if v == "job.status" {
					return rpc.Reply{OK: true, Data: map[string]any{"id": "JOB", "verb": "site.delete", "state": "failed", "code": "precondition_failed", "error": "PRIVATE ERROR", "result": tc.result}}, nil
				}
				return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
			}
			w := httptest.NewRecorder()
			setup(t, f).ServeHTTP(w, request("GET", "/jobs/JOB", ""))
			body := w.Body.String()
			if w.Code != 200 || !strings.Contains(body, "Site deletion failed.") || strings.Contains(body, "attached databases") || strings.Contains(body, "PRIVATE ERROR") || strings.Contains(body, "<script>alert(1)</script>") {
				t.Fatalf("%d %s", w.Code, body)
			}
		})
	}
}

func TestSiteDeleteFailedResult(t *testing.T) {
	f := identityRPC()
	f.reply = func(v string) (rpc.Reply, error) {
		if v == "job.status" {
			return rpc.Reply{OK: true, Data: map[string]any{"id": "JOB", "verb": "site.delete", "state": "failed", "code": "precondition_failed", "error": "PRIVATE ERROR", "result": map[string]any{"attached_database_ids": []any{"01ARZ3NDEKTSV4RRFFQ69G5FAV", `"><script>alert(1)</script>`}}}}, nil
		}
		return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
	}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("GET", "/jobs/JOB", ""))
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "01ARZ3NDEKTSV4RRFFQ69G5FAV") || !strings.Contains(body, "<li>01ARZ3NDEKTSV4RRFFQ69G5FAV</li>") || strings.Contains(body, "hidden data-database-id") || strings.Contains(body, "PRIVATE ERROR") || strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatalf("%d %s", w.Code, body)
	}
}

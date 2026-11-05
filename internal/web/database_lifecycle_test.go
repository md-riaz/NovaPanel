package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"reflect"
	"strings"
	"testing"
)

func TestDatabaseLifecycleBrowserRoutes(t *testing.T) {
	f := identityRPC()
	f.reply = func(v string) (rpc.Reply, error) {
		switch v {
		case "session.whoami":
			return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
		case "job.claim":
			return rpc.Reply{OK: true, Data: map[string]any{"password": "secret"}}, nil
		default:
			return rpc.Reply{OK: true, Data: map[string]any{"job": map[string]any{"id": "JOB", "state": "queued", "verb": v}, "claim": "token"}}, nil
		}
	}
	h := setup(t, f)
	for _, tc := range []struct {
		path, body, verb string
		args             map[string]any
	}{
		{"/databases/create", "name=demo&site_id=SITE&evil=x", "database.create", map[string]any{"name": "demo", "site_id": "SITE"}},
		{"/databases/DB/rotate", "evil=x", "database.rotate_password", map[string]any{"db_id": "DB"}},
		{"/databases/DB/delete", "evil=x", "database.delete", map[string]any{"db_id": "DB"}},
		{"/jobs/JOB/claim", "claim=token&evil=x", "job.claim", map[string]any{"job_id": "JOB", "claim": "token"}},
	} {
		r := request("POST", tc.path, tc.body)
		r.Header.Set("HX-Request", "true")
		r.Header.Set("Idempotency-Key", "01234567-89ab-4def-8123-456789abcdef")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code >= 300 || f.rpcCalls[len(f.rpcCalls)-1].verb != tc.verb || !reflect.DeepEqual(f.rpcCalls[len(f.rpcCalls)-1].args, tc.args) {
			t.Fatalf("%s: %d %s %+v", tc.path, w.Code, w.Body.String(), f.rpcCalls)
		}
		if tc.verb == "database.create" || tc.verb == "database.rotate_password" {
			if w.Header().Get("X-Job-Claim") != "token" || strings.Contains(w.Body.String(), "token") {
				t.Fatal("claim not confined to response header")
			}
		}
		if tc.verb == "job.claim" && !strings.Contains(w.Body.String(), `"password":"secret"`) {
			t.Fatal("claim not delivered")
		}
	}
	r := request("POST", "/databases/create", "")
	w := httptest.NewRecorder()
	before := len(f.rpcCalls)
	h.ServeHTTP(w, r)
	if w.Code != 400 || len(f.rpcCalls) != before+1 {
		t.Fatal("non-JS request must not enqueue")
	}
}

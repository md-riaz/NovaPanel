package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"testing"
)

func TestHTMXLoginRedirectDoesNotLoseHeaderToHTTPRedirect(t *testing.T) {
	f := &fakeRPC{reply: func(v string) (rpc.Reply, error) {
		if v == "session.start" {
			return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_user_secret", "csrf_token": "expected"}}, nil
		}
		return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_new_secret", "csrf_token": "new"}}, nil
	}}
	r := request("POST", "/login", "user=alice&password=secret")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("HTMX ignores redirect headers on 3xx: status %d", w.Code)
	}
}

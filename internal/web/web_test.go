package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

type rpcCall struct {
	verb, session string
	args          any
}
type fakeRPC struct {
	rpcCalls []rpcCall
	closed   int
	reply    func(string) (rpc.Reply, error)
}

func (f *fakeRPC) Call(_ context.Context, v string, a any, s string) (rpc.Reply, error) {
	f.rpcCalls = append(f.rpcCalls, rpcCall{v, s, a})
	return f.reply(v)
}
func (f *fakeRPC) Close() error { f.closed++; return nil }
func setup(t *testing.T, f *fakeRPC) http.Handler {
	t.Helper()
	h, e := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return f, nil }})
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func TestDuplicateSessionCookiesRefused(t *testing.T) {
	f := identityRPC()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "https://panel.example/logout", nil)
	r.Header.Set("Cookie", CookieName+"=nps_a_secret; "+CookieName+"=nps_b_secret")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("X-CSRF-Token", "expected")
	setup(t, f).ServeHTTP(w, r)
	if w.Code != 400 || len(f.rpcCalls) != 0 {
		t.Fatalf("status %d, calls %+v", w.Code, f.rpcCalls)
	}
}
func identityRPC() *fakeRPC {
	return &fakeRPC{reply: func(v string) (rpc.Reply, error) {
		return rpc.Reply{OK: true, Data: map[string]any{"user": "alice", "role": "read_only", "csrf_token": "expected"}}, nil
	}}
}
func TestBootstrapCookieAndLoginShell(t *testing.T) {
	f := &fakeRPC{reply: func(v string) (rpc.Reply, error) {
		if v != "session.start" {
			t.Fatalf("unexpected %s", v)
		}
		return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_anon_secret", "csrf_token": "bootstrap-csrf"}}, nil
	}}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, httptest.NewRequest("GET", "https://panel.example/login", nil))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies %v", cookies)
	}
	c := cookies[0]
	if !strings.HasPrefix(c.Name, "__Host-") || !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("insecure cookie %+v", c)
	}
	if strings.Contains(c.Value, "csrf") {
		t.Fatal("CSRF cookie")
	}
	if !strings.Contains(w.Body.String(), "bootstrap-csrf") || !strings.Contains(w.Body.String(), "hx-post=\"/login\"") {
		t.Fatal("missing header token/form")
	}
	if f.closed != 1 {
		t.Fatal("RPC not closed")
	}
}

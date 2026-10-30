package web

import (
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

func TestLoginBootstrapPreservesAnonymousEnvelope(t *testing.T) {
	f := &fakeRPC{reply: func(string) (rpc.Reply, error) {
		return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_user_secret", "csrf_token": "expected"}}, nil
	}}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("GET", "/login", ""))
	if w.Code != 200 || len(f.rpcCalls) != 1 || f.rpcCalls[0].session != "nps_user_secret" {
		t.Fatalf("status %d calls %+v", w.Code, f.rpcCalls)
	}
}
func TestLoginRefusesInvalidFormsWithoutForwarding(t *testing.T) {
	for _, body := range []string{"", "user=alice", "password=secret", "user=" + strings.Repeat("a", 129) + "&password=secret", "user=alice&password=" + strings.Repeat("a", 1025), "user=alice&password=secret&totp=" + strings.Repeat("a", 129), strings.Repeat("a", 17<<10)} {
		f := &fakeRPC{reply: func(string) (rpc.Reply, error) {
			return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_user_secret", "csrf_token": "expected"}}, nil
		}}
		w := httptest.NewRecorder()
		setup(t, f).ServeHTTP(w, request("POST", "/login", body))
		if w.Code != 400 || len(f.rpcCalls) != 1 {
			t.Fatalf("status %d calls %d", w.Code, len(f.rpcCalls))
		}
	}
}
func TestLoginAuthenticatedCookiePreconditionIsNotDowngraded(t *testing.T) {
	f := &fakeRPC{reply: func(string) (rpc.Reply, error) { return rpc.Reply{Code: "precondition_failed"}, nil }}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("POST", "/login", "user=alice&password=secret"))
	if w.Code != 409 || len(f.rpcCalls) != 1 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("status %d calls %+v", w.Code, f.rpcCalls)
	}
}
func TestLoginNeverEchoesCredentialFailures(t *testing.T) {
	f := &fakeRPC{reply: func(v string) (rpc.Reply, error) {
		if v == "session.start" {
			return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_user_secret", "csrf_token": "expected"}}, nil
		}
		return rpc.Reply{Code: "unauthenticated", Error: "password secret"}, nil
	}}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("POST", "/login", "user=alice&password=secret"))
	if w.Code != 401 || strings.Contains(w.Body.String(), "secret") || len(w.Result().Cookies()) != 0 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
}

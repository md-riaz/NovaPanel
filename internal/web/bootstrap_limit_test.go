package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"novapanel/internal/rpc"
)

func TestCookielessBootstrapClientBudget(t *testing.T) {
	now := time.Unix(1000, 0)
	f := identityRPC()
	f.reply = func(string) (rpc.Reply, error) {
		return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_anon", "csrf_token": "expected"}}, nil
	}
	h, err := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return f, nil }, RateLimitRPS: 2, RateLimitBurst: 2, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := func(addr, cookie string, want int) {
		t.Helper()
		r := httptest.NewRequest("GET", "https://panel.example/login", nil)
		r.RemoteAddr = addr
		r.Header.Set("X-Forwarded-For", fmt.Sprint(len(f.rpcCalls)))
		r.Header.Set("X-Real-IP", fmt.Sprint(len(f.rpcCalls)))
		r.Header.Set("Forwarded", fmt.Sprintf("for=192.0.2.%d", len(f.rpcCalls)))
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		}
		before := len(f.rpcCalls)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("addr=%s cookie=%s got=%d want=%d", addr, cookie, w.Code, want)
		}
		if want == 429 && (len(f.rpcCalls) != before || w.Header().Get("Retry-After") == "" || w.Header().Get("Content-Security-Policy") == "") {
			t.Fatal("refusal dialed RPC or lost security headers")
		}
	}
	request("192.0.2.1:1000", "", 200)
	request("192.0.2.1:2000", "", 200)
	request("192.0.2.1:3000", "", 429)
	request("192.0.2.2:1000", "", 200)
	// Cookie and client keys occupy different namespaces even when their text matches.
	request("192.0.2.1:4000", "192.0.2.1", 200)
	request("192.0.2.2:2000", "192.0.2.1", 200)
	request("192.0.2.3:1000", "192.0.2.1", 429)
	now = now.Add(499 * time.Millisecond)
	request("192.0.2.1:5000", "", 429)
	now = now.Add(time.Millisecond)
	request("192.0.2.1:5001", "", 200)
	request("192.0.2.1:5002", "", 429)
	now = now.Add(-time.Second)
	request("192.0.2.1:5003", "", 429)
	// IPv6 textual variants and ports must not create separate budgets.
	request("[2001:db8::1]:1000", "", 200)
	request("[2001:0db8:0:0:0:0:0:1]:2000", "", 200)
	request("[2001:db8::1]:3000", "", 429)
}

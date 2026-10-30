package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"novapanel/internal/rpc"
	"strings"
	"testing"
)

func request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "https://panel.example"+path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "nps_user_secret"})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("X-CSRF-Token", "expected")
	return r
}
func TestCSRFAndOriginBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, csrf, fetch, origin, referer string
		want                               int
	}{
		{"valid", "expected", "same-origin", "", "", 303},
		{"missing", "", "same-origin", "", "", 403}, {"wrong", "wrong", "same-origin", "", "", 403},
		{"cross-site", "expected", "cross-site", "https://panel.example", "", 403}, {"same-site", "expected", "same-site", "", "", 403},
		{"origin fallback", "expected", "", "https://panel.example", "", 303}, {"referer fallback", "expected", "", "", "https://panel.example/mfa", 303},
		{"missing fallback", "expected", "", "", "", 403}, {"host suffix", "expected", "", "https://panel.example.evil", "", 403},
		{"origin mismatch", "expected", "same-origin", "https://evil.example", "", 403}, {"null origin", "expected", "", "null", "", 403},
		{"scheme mismatch", "expected", "", "http://panel.example", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := identityRPC()
			w := httptest.NewRecorder()
			r := request("POST", "/logout", "")
			r.Header.Set("X-CSRF-Token", tc.csrf)
			r.Header.Set("Sec-Fetch-Site", tc.fetch)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Referer", tc.referer)
			setup(t, f).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			for _, c := range f.rpcCalls {
				if c.verb == "session.logout" && tc.want != 303 {
					t.Fatal("mutation escaped guard")
				}
				if c.session != "nps_user_secret" {
					t.Fatal("session not forwarded whole")
				}
			}
		})
	}
}
func TestNoMutationOnSafeOrUnknownRoutes(t *testing.T) {
	for _, p := range []string{"/logout", "/mfa/enroll", "/mfa/confirm", "/mfa/disable", "/reauth", "/unknown"} {
		f := identityRPC()
		w := httptest.NewRecorder()
		setup(t, f).ServeHTTP(w, request("GET", p, ""))
		for _, c := range f.rpcCalls {
			if c.verb != "session.whoami" {
				t.Fatalf("GET %s mutated with %s", p, c.verb)
			}
		}
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE", "TRACE", "HEAD", "OPTIONS"} {
		f := identityRPC()
		w := httptest.NewRecorder()
		setup(t, f).ServeHTTP(w, request(method, "/logout", ""))
		if len(f.rpcCalls) != 0 {
			t.Fatalf("%s reached RPC", method)
		}
	}
	for _, r := range routes {
		if r.auth != "user" && r.auth != "bootstrap" && r.auth != "anonymous-blocked" {
			t.Fatalf("route lacks decision %+v", r)
		}
	}
}
func TestLoginGapFailsClosed(t *testing.T) {
	f := identityRPC()
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("POST", "/login", "user=alice&password=secret"))
	if w.Code != 503 || len(f.rpcCalls) != 0 {
		t.Fatalf("blocked anon seam status=%d calls=%+v", w.Code, f.rpcCalls)
	}
}
func TestDashboardReadOnlyAndEscaped(t *testing.T) {
	f := identityRPC()
	f.reply = func(v string) (rpc.Reply, error) {
		if v == "session.whoami" {
			return rpc.Reply{OK: true, Data: map[string]any{"user": "<script>bad()</script>", "role": "read_only", "csrf_token": "expected"}}, nil
		}
		return rpc.Reply{OK: false, Code: "unsupported"}, errors.New("deferred")
	}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("GET", "/", ""))
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>bad()") || !strings.Contains(w.Body.String(), "&lt;script&gt;") || !strings.Contains(w.Body.String(), "Unavailable in this phase") {
		t.Fatalf("body %s", w.Body.String())
	}
	want := []string{"session.whoami", "system.status", "system.metrics"}
	if len(f.rpcCalls) != len(want) {
		t.Fatal(f.rpcCalls)
	}
	for i, c := range f.rpcCalls {
		if c.verb != want[i] {
			t.Fatal(f.rpcCalls)
		}
	}
}
func TestReauthRotation(t *testing.T) {
	f := identityRPC()
	old := f.reply
	f.reply = func(v string) (rpc.Reply, error) {
		if v == "session.reauth" {
			return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_rotated_secret", "csrf_token": "rotated-csrf", "mfa_at": 123}}, nil
		}
		return old(v)
	}
	w := httptest.NewRecorder()
	setup(t, f).ServeHTTP(w, request("POST", "/reauth", "totp=123456"))
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Value != "nps_rotated_secret" || !strings.Contains(w.Body.String(), "rotated-csrf") {
		t.Fatalf("rotation status=%d body=%s", w.Code, w.Body.String())
	}
	if fmt.Sprint(f.rpcCalls[1].args) != "map[totp:123456]" {
		t.Fatal(f.rpcCalls)
	}
}
func TestMFAForwardingShowOnce(t *testing.T) {
	for _, tc := range []struct {
		path, verb string
		data       map[string]any
		text       string
	}{
		{"/mfa/enroll", "mfa.enroll", map[string]any{"otpauth_uri": "otpauth://totp/Panel?secret=TRANSIENT"}, "TRANSIENT"},
		{"/mfa/confirm", "mfa.confirm", map[string]any{"backup_codes": []string{"backup-one", "backup-two"}}, "backup-one"},
		{"/mfa/disable", "mfa.disable", map[string]any{}, "MFA disabled"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			f := identityRPC()
			old := f.reply
			f.reply = func(v string) (rpc.Reply, error) {
				if v == tc.verb {
					return rpc.Reply{OK: true, Data: tc.data}, nil
				}
				return old(v)
			}
			h := setup(t, f)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request("POST", tc.path, "totp=123456&user_id=someone-else"))
			if w.Code != 200 || !strings.Contains(w.Body.String(), tc.text) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(fmt.Sprint(f.rpcCalls), "someone-else") {
				t.Fatal("cross-user parameter forwarded")
			}
			if len(w.Result().Cookies()) != 0 {
				t.Fatal("secret stored in cookie")
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, request("GET", "/mfa", ""))
			if strings.Contains(w.Body.String(), tc.text) {
				t.Fatal("show-once data retained")
			}
		})
	}
}
func TestAgentFailureAndReauthHint(t *testing.T) {
	for _, tc := range []struct {
		code   string
		hint   bool
		status int
	}{{"unauthenticated", false, 401}, {"forbidden", true, 403}, {"agent_error", false, 502}} {
		f := identityRPC()
		f.reply = func(string) (rpc.Reply, error) {
			return rpc.Reply{Code: tc.code, Data: map[string]any{"reauth_required": tc.hint}}, errors.New("DO NOT LEAK")
		}
		w := httptest.NewRecorder()
		setup(t, f).ServeHTTP(w, request("GET", "/mfa", ""))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "DO NOT LEAK") || f.closed != 1 {
			t.Fatalf("response %d %s", w.Code, w.Body.String())
		}
		if tc.hint && !strings.Contains(w.Body.String(), "/reauth") {
			t.Fatal("lost hint")
		}
	}
}
func TestSecurityHeadersAndSelfHostedAssets(t *testing.T) {
	for _, path := range []string{"/assets/app.js", "/assets/app.css", "/assets/htmx.min.js", "/assets/missing", "/missing"} {
		f := identityRPC()
		w := httptest.NewRecorder()
		setup(t, f).ServeHTTP(w, httptest.NewRequest("GET", "https://panel.example"+path, nil))
		for _, h := range []string{"Content-Security-Policy", "Strict-Transport-Security", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy", "Cache-Control"} {
			if w.Header().Get(h) == "" {
				t.Fatalf("%s lacks %s", path, h)
			}
		}
		if strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline") {
			t.Fatal("unsafe CSP")
		}
		if len(f.rpcCalls) != 0 {
			t.Fatal("asset/unknown route reached RPC")
		}
	}
}
func TestLogOnlyStillRequiresCSRF(t *testing.T) {
	f := identityRPC()
	violations := 0
	h, e := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return f, nil }, LogOnlyOrigin: true, OriginViolation: func() { violations++ }})
	if e != nil {
		t.Fatal(e)
	}
	r := request("POST", "/logout", "")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Del("X-CSRF-Token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || violations != 1 || len(f.rpcCalls) != 1 {
		t.Fatalf("status %d calls %v violations %d", w.Code, f.rpcCalls, violations)
	}
}
func TestMutationRequiresFormContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/plain", "application/json", "application/x-www-form-urlencoded; broken"} {
		f := identityRPC()
		w := httptest.NewRecorder()
		r := request("POST", "/logout", "")
		r.Header.Set("Content-Type", contentType)
		setup(t, f).ServeHTTP(w, r)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("content type %q: status %d", contentType, w.Code)
		}
		for _, call := range f.rpcCalls {
			if call.verb == "session.logout" {
				t.Fatal("non-form request mutated")
			}
		}
	}
}

func TestLargeFormAndQueryCodeRefused(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", 17<<10), ""} {
		f := identityRPC()
		w := httptest.NewRecorder()
		r := request("POST", "/reauth?totp=query-secret", body)
		setup(t, f).ServeHTTP(w, r)
		if w.Code != 400 || len(f.rpcCalls) != 1 {
			t.Fatalf("invalid form reached mutation: %d %+v", w.Code, f.rpcCalls)
		}
	}
}

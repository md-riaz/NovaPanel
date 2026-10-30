package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"novapanel/internal/rpc"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransportIsolationRefillAndCanonicalKey(t *testing.T) {
	now := time.Unix(1000, 0)
	f := identityRPC()
	h, err := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return f, nil }, RateLimitRPS: 2, RateLimitBurst: 2, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	request := func(token, path, method string, want int) {
		t.Helper()
		r := httptest.NewRequest(method, "https://panel.example"+path, nil)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
		}
		r.Header.Set("Authorization", fmt.Sprintf("Bearer spoof-%d", len(f.rpcCalls)))
		r.Header.Set("X-Forwarded-For", fmt.Sprint(len(f.rpcCalls)))
		r.Header.Set("X-Role", "owner")
		w := httptest.NewRecorder()
		before := len(f.rpcCalls)
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s token=%s status=%d want=%d", method, path, token, w.Code, want)
		}
		if want == 429 && len(f.rpcCalls) != before {
			t.Fatal("refusal reached RPC")
		}
	}
	for _, token := range []string{"nps_session_a", "nps_session_b", "npat_token_a"} {
		request(token, "/", "GET", 200)
		request(token, "/reauth", "GET", 200)
		request(token, "/mfa", "GET", 429)
	}
	request("nps_session_a", "/logout", "POST", 403)
	request("nps_session_a", "/assets/app.js", "GET", 200)
	request("nps_session_a", "/missing", "GET", 404)
	now = now.Add(499 * time.Millisecond)
	request("nps_session_a", "/", "GET", 429)
	now = now.Add(time.Millisecond)
	request("nps_session_a", "/", "GET", 200)
	request("nps_session_a", "/", "GET", 429)
	now = now.Add(-time.Second)
	request("nps_session_a", "/", "GET", 429)
	now = now.Add(time.Hour)
	request("nps_session_a", "/", "GET", 200)
	request("nps_session_a", "/", "GET", 200)
	request("nps_session_a", "/", "GET", 429)
	// No invented IP/role/header budget for cookie-less requests.
	f.reply = func(string) (rpc.Reply, error) {
		return rpc.Reply{OK: true, Data: map[string]any{"session": "nps_anon", "csrf_token": "expected"}}, nil
	}
	for i := 0; i < 25; i++ {
		request("", "/login", "GET", 200)
	}
}

func TestTransportConcurrentAdmissionBeforeDial(t *testing.T) {
	var dials, admitted atomic.Int64
	now := time.Unix(1000, 0)
	h, err := New(Options{Origin: "https://panel.example", RateLimitRPS: 1, RateLimitBurst: 20, Now: func() time.Time { return now }, Dial: func() (RPCClient, error) { dials.Add(1); return nil, fmt.Errorf("unavailable") }})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("GET", "https://panel.example/", nil)
			r.AddCookie(&http.Cookie{Name: CookieName, Value: "nps_shared"})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code == 503 {
				admitted.Add(1)
			} else if w.Code != 429 {
				t.Errorf("status=%d", w.Code)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 20 || dials.Load() != 20 {
		t.Fatalf("admitted=%d dials=%d", admitted.Load(), dials.Load())
	}
}

func TestTransportCacheExhaustionDoesNotRestoreAllowance(t *testing.T) {
	l := transportLimiter{buckets: make(map[[32]byte]transportBucket), rps: 1, burst: 2}
	now := time.Unix(1000, 0)
	for i := 0; i < maxTransportBuckets; i++ {
		if !l.allow(fmt.Sprint(i), now) {
			t.Fatal("early refusal")
		}
	}
	if l.allow("overflow", now) || len(l.buckets) != maxTransportBuckets {
		t.Fatal("cache overflow admitted or grew")
	}
	if !l.allow("0", now) || l.allow("0", now) {
		t.Fatal("active budget was evicted")
	}
	now = now.Add(time.Second)
	if !l.allow("overflow", now) {
		t.Fatal("fully replenished entries not cleaned")
	}
	if !l.allow("0", now) || l.allow("0", now) {
		t.Fatal("cleanup restored active allowance")
	}
	if len(l.buckets) > maxTransportBuckets {
		t.Fatal("unbounded cache")
	}
}

func TestTransportDefaultBurst(t *testing.T) {
	f := identityRPC()
	h := setup(t, f)
	statuses := map[int]int{}
	for i := 0; i < 100; i++ {
		r := httptest.NewRequest("GET", "https://panel.example/", nil)
		r.AddCookie(&http.Cookie{Name: CookieName, Value: "nps_same_session"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		statuses[w.Code]++
		if i >= 20 && (w.Code != 429 || w.Header().Get("Retry-After") == "" || w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Access-Control-Allow-Origin") != "") {
			t.Fatalf("request %d: status=%d headers=%v", i, w.Code, w.Header())
		}
	}
	t.Logf("100 immediate requests: statuses=%v RPC calls=%d", statuses, len(f.rpcCalls))
	if statuses[200] != 20 || statuses[429] != 80 || len(f.rpcCalls) != 60 || f.closed != 20 {
		t.Fatalf("statuses=%v RPC calls=%d closed=%d", statuses, len(f.rpcCalls), f.closed)
	}
}

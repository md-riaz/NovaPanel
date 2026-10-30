package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/auth"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	_ "novapanel/internal/verbs"
	"novapanel/internal/web"
)

type browserClock struct{ now atomic.Int64 }

func (c *browserClock) Now() int64 { return c.now.Load() }

type browserRPC struct{ s *rpc.Server }

func (c browserRPC) Close() error { return nil }
func (c browserRPC) Call(ctx context.Context, verb string, args any, token string) (rpc.Reply, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return rpc.Reply{}, err
	}
	rid, err := id.New()
	if err != nil {
		return rpc.Reply{}, err
	}
	// Inject the unprivileged web identity, never the host's uid-0 implicit owner.
	return c.s.Dispatch(rpc.WithCaller(ctx, rpc.Caller{Peer: 1001}), rpc.Request{ID: rid, Verb: verb, Args: raw, Session: token}), nil
}

// Opt-in real Chromium acceptance. All state is disposable; no daemon or host command is used.
func TestBrowserAuthentication(t *testing.T) {
	node := os.Getenv("NOVAPANEL_BROWSER_NODE")
	if node == "" {
		t.Skip("set NOVAPANEL_BROWSER_NODE to run real Chromium acceptance")
	}
	clock := &browserClock{}
	clock.now.Store(time.Now().UnixMilli())
	st, err := store.OpenWithClock(filepath.Join(t.TempDir(), "browser.db"), clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := agent.New()
	a.Clock = clock
	a.Store = st
	a.Secret, err = secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := id.New()
	if err = st.CreateUser(context.Background(), store.User{ID: uid, Username: "reader", Email: "reader@example.test", PWPHC: auth.Hash("browser fixture password"), Role: "read_only"}); err != nil {
		t.Fatal(err)
	}
	s := rpc.NewServer(a)
	s.WebUID = 1001
	var mu sync.Mutex
	var events []rpc.AuditEvent
	s.Audit = rpc.AuditFunc(func(e rpc.AuditEvent) { mu.Lock(); defer mu.Unlock(); events = append(events, e) })
	ts := httptest.NewUnstartedServer(nil)
	ts.StartTLS()
	defer ts.Close()
	h, err := web.New(web.Options{Origin: ts.URL, Dial: func() (web.RPCClient, error) { return browserRPC{s}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Test-only clock advance. This endpoint never exists in the production router.
		if r.URL.Path == "/__test/expire" && r.Method == "POST" {
			clock.now.Add(601000)
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/__test/forbidden" && r.Method == "POST" {
			cookie, err := r.Cookie(web.CookieName)
			if err != nil {
				w.WriteHeader(401)
				return
			}
			reply, _ := (browserRPC{s}).Call(r.Context(), "user.create", map[string]any{"username": "forbidden", "email": "bad@example.test", "role": "admin", "password": "never created"}, cookie.Value)
			if reply.Code != "forbidden" {
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(403)
			return
		}
		h.ServeHTTP(w, r)
	})
	cmd := exec.Command(node, "../browser.cjs", ts.URL)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatalf("browser: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	raw, _ := json.Marshal(events)
	for _, value := range []string{"browser fixture password", "otpauth://", "nps_", "backup_codes", "csrf_token"} {
		if strings.Contains(string(raw), value) {
			t.Fatalf("audit leaked %s", value)
		}
	}
	successes, failures := 0, 0
	for _, e := range events {
		if e.Verb == "session.login" {
			if e.Outcome == "ok" {
				successes++
			} else {
				failures++
			}
		}
	}
	if successes < 1 || failures < 1 {
		t.Fatalf("login audits success=%d failure=%d", successes, failures)
	}
	t.Logf("secret-free audit verified: %d events", len(events))
}

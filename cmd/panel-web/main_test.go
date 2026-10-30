package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPServerConfigured(t *testing.T) {
	oldUID, oldServe := geteuid, serveHTTP
	defer func() { geteuid = oldUID; serveHTTP = oldServe }()
	geteuid = func() int { return 1001 }
	called := false
	serveHTTP = func(s *http.Server) error {
		called = true
		if s.Addr != "127.0.0.1:8080" || s.Handler == nil || s.ReadHeaderTimeout == 0 || s.IdleTimeout == 0 {
			t.Fatal("unbounded/missing HTTP server")
		}
		return nil
	}
	if e := run(); e != nil {
		t.Fatal(e)
	}
	if !called {
		t.Fatal("HTTP server not started")
	}
}

func TestTransportConfig(t *testing.T) {
	oldUID, oldServe := geteuid, serveHTTP
	defer func() { geteuid, serveHTTP = oldUID, oldServe }()
	geteuid = func() int { return 1001 }
	path := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(path, []byte("rate_limit_rps = 1\nrate_limit_burst = 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOVAPANEL_CONFIG", path)
	t.Setenv("NOVAPANEL_SOCKET_PATH", filepath.Join(t.TempDir(), "missing.sock"))
	for _, burst := range []int{2, 1} {
		if burst == 1 {
			t.Setenv("NOVAPANEL_RATE_LIMIT_BURST", "1")
		}
		serveHTTP = func(s *http.Server) error {
			for i := 0; i <= burst; i++ {
				r := httptest.NewRequest("GET", "https://localhost/", nil)
				r.AddCookie(&http.Cookie{Name: "__Host-novapanel", Value: "nps_config_test"})
				w := httptest.NewRecorder()
				s.Handler.ServeHTTP(w, r)
				want := 503
				if i == burst {
					want = 429
				}
				if w.Code != want {
					t.Fatalf("burst=%d request=%d status=%d want=%d", burst, i, w.Code, want)
				}
			}
			return nil
		}
		if err := run(); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("NOVAPANEL_RATE_LIMIT_RPS", "0")
	if err := run(); err == nil || !strings.Contains(err.Error(), "rate_limit_rps") {
		t.Fatalf("invalid config: %v", err)
	}
}

func TestRefusesRoot(t *testing.T) {
	old, oldServe := geteuid, serveHTTP
	defer func() { serveHTTP = oldServe }()
	serveHTTP = func(*http.Server) error { return nil }
	defer func() { geteuid = old }()
	geteuid = func() int { return 0 }
	if err := run(); err == nil {
		t.Fatal("web started as root")
	}
	geteuid = func() int { return 1001 }
	if err := run(); err != nil {
		t.Fatal(err)
	}
}

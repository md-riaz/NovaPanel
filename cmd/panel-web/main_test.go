package main

import (
	"net/http"
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

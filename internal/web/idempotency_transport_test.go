package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"novapanel/internal/rpc"
	"path/filepath"
	"testing"
)

func TestDatabaseSubmissionKeyValidation(t *testing.T) {
	for _, key := range []string{"", "bad", "01234567-89ab-4def-8123-456789abcdeF", "01234567-89ab-4def-8123-456789abcdef,other"} {
		f := identityRPC()
		f.reply = func(v string) (rpc.Reply, error) {
			return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
		}
		r := request("POST", "/databases/delete-id/delete", "")
		r.Header.Set("HX-Request", "true")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		setup(t, f).ServeHTTP(w, r)
		if w.Code != 400 || len(f.rpcCalls) != 1 {
			t.Fatalf("key %q: status %d calls %+v", key, w.Code, f.rpcCalls)
		}
	}
}

func TestDatabaseDeleteWithoutJavaScript(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{"csrf_token=expected", 202}, {"csrf_token=wrong", 403}, {"", 403}, {"csrf_token=expected&csrf_token=expected", 403},
	} {
		f := identityRPC()
		f.reply = func(v string) (rpc.Reply, error) {
			if v == "database.delete" {
				return rpc.Reply{OK: true, Data: map[string]any{"job": map[string]any{"id": "JOB", "state": "queued"}}}, nil
			}
			return rpc.Reply{OK: true, Data: map[string]any{"role": "admin", "csrf_token": "expected"}}, nil
		}
		r := request("POST", "/databases/delete-id/delete", tc.body)
		r.Header.Del("X-CSRF-Token")
		w := httptest.NewRecorder()
		setup(t, f).ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("body %q: status %d: %s", tc.body, w.Code, w.Body.String())
		}
		if (len(f.rpcCalls) == 2) != (tc.status == 202) {
			t.Fatalf("body %q: calls %+v", tc.body, f.rpcCalls)
		}
	}
}

func TestDatabaseLostReplyPreservesKeyOverRPC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		for attempt := 0; attempt < 2; attempt++ {
			conn, err := ln.Accept()
			if err != nil {
				done <- err
				return
			}
			scanner := bufio.NewScanner(conn)
			for scanner.Scan() {
				var req rpc.Request
				if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
					conn.Close()
					done <- err
					return
				}
				if req.Verb == "session.whoami" {
					fmt.Fprintf(conn, `{"id":%q,"ok":true,"data":{"role":"admin","csrf_token":"expected"}}`+"\n", req.ID)
					continue
				}
				if req.Verb != "database.create" || req.IdempotencyKey != "01234567-89ab-4def-8123-456789abcdef" || string(req.Args) != `{"name":"demo"}` {
					conn.Close()
					done <- fmt.Errorf("unexpected request: %+v", req)
					return
				}
				if attempt == 1 {
					fmt.Fprintf(conn, `{"id":%q,"ok":true,"data":{"job":{"id":"JOB","state":"queued"},"claim":"claim"}}`+"\n", req.ID)
				}
				break
			}
			conn.Close()
		}
		done <- nil
	}()
	h, err := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return rpc.Dial(path) }})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		r := request("POST", "/databases/create", "name=demo")
		r.Header.Set("HX-Request", "true")
		r.Header.Set("Idempotency-Key", "01234567-89ab-4def-8123-456789abcdef")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if attempt == 0 && w.Code != 502 || attempt == 1 && (w.Code != 202 || w.Header().Get("X-Job-Claim") != "claim") {
			t.Fatalf("attempt %d: %d %s", attempt, w.Code, w.Body.String())
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

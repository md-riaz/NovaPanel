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

func TestPHPSetRealRPCProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		conn, e := ln.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		scan := bufio.NewScanner(conn)
		for _, verb := range []string{"session.whoami", "php.set"} {
			if !scan.Scan() {
				done <- fmt.Errorf("missing %s: %v", verb, scan.Err())
				return
			}
			var req rpc.Request
			if e = json.Unmarshal(scan.Bytes(), &req); e != nil {
				done <- e
				return
			}
			if req.Verb != verb || req.Session != "nps_user_secret" {
				done <- fmt.Errorf("bad envelope: %+v", req)
				return
			}
			var data any = map[string]any{"role": "admin", "csrf_token": "expected"}
			if verb == "php.set" {
				var args map[string]any
				if e = json.Unmarshal(req.Args, &args); e != nil {
					done <- e
					return
				}
				limits, ok := args["limits"].(map[string]any)
				if !ok || len(args) != 3 || args["site_id"] != "SITE" || args["version"] != "8.3" || len(limits) != 3 || limits["memory_mb"] != float64(512) || limits["upload_mb"] != float64(64) || limits["max_exec_sec"] != float64(60) {
					done <- fmt.Errorf("bad args: %v", args)
					return
				}
				data = map[string]any{"job": map[string]any{"id": "JOB", "state": "queued", "verb": "php.set"}}
			}
			e = json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "ok": true, "data": data})
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	h, err := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return rpc.Dial(path) }})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("POST", "/sites/SITE/php", "version=8.3&memory_mb=512&upload_mb=64&max_exec_sec=60"))
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

package web

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http/httptest"
	"novapanel/internal/rpc"
	"path/filepath"
	"testing"
)

// Exercise the existing NDJSON client, not only the injected consumer seam.
func TestRealRPCRequestConnectionClosesOnMalformedReply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	ln, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
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
		if !scan.Scan() {
			done <- scan.Err()
			return
		}
		var req rpc.Request
		if e = json.Unmarshal(scan.Bytes(), &req); e != nil {
			done <- e
			return
		}
		if req.Verb != "session.whoami" || req.Session != "nps_user_secret" {
			done <- &protocolError{}
			return
		}
		_, e = conn.Write([]byte("not-json\n"))
		if e != nil {
			done <- e
			return
		}
		if scan.Scan() {
			done <- &protocolError{}
			return
		}
		done <- scan.Err()
	}()
	h, e := New(Options{Origin: "https://panel.example", Dial: func() (RPCClient, error) { return rpc.Dial(path) }})
	if e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request("GET", "/mfa", ""))
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

type protocolError struct{}

func (*protocolError) Error() string {
	return "unexpected RPC request after malformed reply or wrong envelope"
}

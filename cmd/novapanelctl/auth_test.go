package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"novapanel/internal/rpc"
	"path/filepath"
	"testing"
)

func TestCtlCanonicalUserCreateFromStdin(t *testing.T) {
	l, e := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	done := make(chan rpc.Request, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		scan := bufio.NewScanner(c)
		if !scan.Scan() {
			return
		}
		req, e := rpc.DecodeLine(scan.Bytes())
		if e != nil {
			return
		}
		r := rpc.OK(map[string]string{"user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV"})
		r.ID = req.ID
		_ = rpc.Encode(c, r)
		done <- req
	}()
	payload := `{"username":"first","email":"first@example.test","role":"owner","password":"stdin-only-password"}`
	var out bytes.Buffer
	if e = runWithInput(context.Background(), []string{"-socket", l.Addr().String(), "-args-stdin", "user.create"}, bytes.NewBufferString(payload), &out); e != nil {
		t.Fatal(e)
	}
	req := <-done
	var args map[string]string
	_ = json.Unmarshal(req.Args, &args)
	if req.Verb != "user.create" || args["password"] != "stdin-only-password" || req.Session != "" {
		t.Fatalf("request %+v", req)
	}
	if bytes.Contains(out.Bytes(), []byte("stdin-only-password")) {
		t.Fatal("password echoed")
	}
	for _, input := range []string{`[]`, `null`, `{} {}`, `{`} {
		if e = runWithInput(context.Background(), []string{"-args-stdin", "user.create"}, bytes.NewBufferString(input), &out); e == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

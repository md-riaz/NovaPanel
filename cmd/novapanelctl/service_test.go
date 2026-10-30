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

func TestCtlServiceUnit(t *testing.T) {
	l, e := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	done := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		scan := bufio.NewScanner(c)
		if !scan.Scan() {
			done <- scan.Err()
			return
		}
		req, e := rpc.DecodeLine(scan.Bytes())
		if e != nil {
			done <- e
			return
		}
		var args struct {
			Unit string `json:"unit"`
		}
		e = json.Unmarshal(req.Args, &args)
		if e != nil {
			done <- e
			return
		}
		r := rpc.OK(map[string]string{"unit": args.Unit})
		r.ID = req.ID
		done <- rpc.Encode(c, r)
	}()
	var out bytes.Buffer
	if e = run(context.Background(), []string{"-socket", l.Addr().String(), "service.status", "nginx"}, &out); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	var r struct {
		OK   bool `json:"ok"`
		Data struct {
			Unit string `json:"unit"`
		} `json:"data"`
	}
	if e = json.Unmarshal(out.Bytes(), &r); e != nil || !r.OK || r.Data.Unit != "nginx.service" {
		t.Fatalf("%s %v", out.String(), e)
	}
}

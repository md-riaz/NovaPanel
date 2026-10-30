package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/rpc"
	"path/filepath"
	"testing"
)

type ctlRunner struct{}

func (ctlRunner) Run(context.Context, string, []string, exec.RunOpts) ([]byte, []byte, int, error) {
	return []byte("active"), nil, 0, nil
}
func TestCtlServiceUnit(t *testing.T) {
	l, e := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	a := agent.New()
	a.Exec = ctlRunner{}
	s := rpc.NewServer(a)
	s.WebUID = ^uint32(0)
	go func() { _ = s.Serve(l) }()
	var out bytes.Buffer
	if e = run(context.Background(), []string{"-socket", l.Addr().String(), "service.status", "nginx"}, &out); e != nil {
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

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"novapanel/internal/agent"
	"novapanel/internal/rpc"
	_ "novapanel/internal/verbs"
	"os"
	"path/filepath"
	"testing"
)

func TestCtlPing(t *testing.T) {
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s := rpc.NewServer(agent.New())
	s.WebUID = uint32(os.Geteuid())
	go func() { _ = s.Serve(l) }()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-socket", l.Addr().String(), "ping"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"version"`)) {
		t.Fatalf("output %s", out.String())
	}
	if err := run(context.Background(), nil, io.Discard); err == nil {
		t.Fatal("missing verb accepted")
	}
}

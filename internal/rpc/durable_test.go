package rpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/audit"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"path/filepath"
	"testing"
)

type failingSink struct{}

func (failingSink) Append([]byte) error       { return errors.New("disk unavailable") }
func (failingSink) Close() error              { return nil }
func (failingSink) Sync() error               { return nil }
func (failingSink) Write([]byte) (int, error) { return 0, errors.New("disk unavailable") }
func TestNewServerWiresDurableAudit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := agent.New()
	a.Audit = audit.New(st, failingSink{})
	s := rpc.NewServer(a)
	if s.DurableAudit == nil {
		t.Fatal("durable audit not wired")
	}
	ran := false
	s.Registry = map[string]rpc.Verb{"mutate": {Name: "mutate", MinRole: rpc.Admin, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { ran = true; return nil, nil }}}
	r := s.Dispatch(rpc.WithCaller(context.Background(), rpc.RootCaller(0)), rpc.Request{Verb: "mutate", Args: json.RawMessage(`{}`)})
	if r.Code != "agent_error" || ran {
		t.Fatalf("audit failure admitted mutation: %+v ran=%v", r, ran)
	}
}

package rpc

import (
	"context"
	"encoding/json"
	"net"
	"novapanel/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerFramingAndPeerGate(t *testing.T) {
	for _, frame := range []string{`{"id":"one","verb":"ping","args":{}}` + "\n", "broken\n", strings.Repeat("x", MaxRequestBytes+1) + "\n"} {
		s := NewServer(agent.New())
		s.WebUID = uint32(os.Geteuid())
		s.Registry = map[string]Verb{"ping": {Name: "ping", Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { return "pong", nil }}}
		l, err := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = s.Serve(l) }()
		c, err := net.Dial("unix", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(time.Second))
		_, _ = c.Write([]byte(frame + `{"id":"two","verb":"ping","args":{}}` + "\n"))
		var r Reply
		err = json.NewDecoder(c).Decode(&r)
		if strings.HasPrefix(frame, `{"id"`) {
			if err != nil || r.ID != "one" || !r.OK {
				t.Fatalf("reply %+v %v", r, err)
			}
		} else if err == nil {
			t.Fatalf("malformed frame answered %+v", r)
		}
		_ = c.Close()
		_ = l.Close()
	}
	if PeerAllowed(1002, 1001) || !PeerAllowed(1001, 1001) || !PeerAllowed(0, 1001) {
		t.Fatal("peer gate")
	}
}
func TestClientRoundTripAndCodedFailure(t *testing.T) {
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "agent.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	s := NewServer(agent.New())
	s.WebUID = uint32(os.Geteuid())
	s.Registry = map[string]Verb{"ping": {Name: "ping", Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) {
		return map[string]string{"version": "test"}, nil
	}}}
	go func() { _ = s.Serve(l) }()
	c, err := Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if r, err := c.Call(context.Background(), "ping", struct{}{}, ""); err != nil || !r.OK || r.ID == "" {
		t.Fatalf("call %+v %v", r, err)
	}
	if r, err := c.Call(context.Background(), "missing", struct{}{}, ""); err == nil || r.Code != "unknown_verb" {
		t.Fatalf("error %+v %v", r, err)
	}
}

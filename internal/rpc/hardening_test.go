package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"novapanel/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStrictNDJSON(t *testing.T) {
	for _, frame := range []string{`{"verb":"ping","args":{}}`, `{"verb":"ping","args":{},"args":[]}` + "\n"} {
		s := NewServer(agent.New())
		s.WebUID = uint32(os.Geteuid())
		s.Registry = map[string]Verb{"ping": {Name: "ping", Public: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { return "pong", nil }}}
		l, err := net.Listen("unix", filepath.Join(t.TempDir(), "socket"))
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = s.Serve(l) }()
		c, err := net.DialUnix("unix", nil, l.Addr().(*net.UnixAddr))
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(time.Second))
		_, _ = c.Write([]byte(frame))
		_ = c.CloseWrite()
		var r Reply
		if err = json.NewDecoder(c).Decode(&r); err == nil {
			t.Errorf("answered malformed or unterminated frame %q", frame)
		} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Errorf("malformed connection remained open: %v", err)
		}
		c.Close()
		l.Close()
	}
}
func TestOversizedFallbackIDIsBounded(t *testing.T) {
	var out strings.Builder
	r := OK(strings.Repeat("x", MaxReplyBytes))
	r.ID = strings.Repeat("\x00", MaxRequestBytes)
	if err := Encode(&out, r); err != nil {
		t.Fatal(err)
	}
	var fallback Reply
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &fallback); err != nil || fallback.OK || fallback.Code != "agent_error" {
		t.Fatalf("invalid fallback: %+v %v", fallback, err)
	}
	if out.Len() > MaxReplyBytes {
		t.Fatal("oversized fallback")
	}
}
func TestClientRejectsUnterminatedReply(t *testing.T) {
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var req Request
		if json.NewDecoder(c).Decode(&req) != nil {
			return
		}
		r := OK("pong")
		r.ID = req.ID
		b, _ := json.Marshal(r)
		_, _ = c.Write(b)
	}()
	c, err := Dial(l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if r, err := c.Call(ctx, "ping", struct{}{}, ""); err == nil {
		t.Fatalf("accepted unterminated reply: %+v", r)
	}
}

func TestFrameLimitsAndTermination(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit int
	}{{"request", MaxRequestBytes}, {"reply", MaxReplyBytes}} {
		for _, n := range []int{tc.limit - 1, tc.limit, tc.limit + 1} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, n), func(t *testing.T) {
				payload := strings.Repeat("x", n)
				scanner := bufio.NewScanner(strings.NewReader(payload + "\n"))
				scanner.Buffer(make([]byte, 4096), tc.limit+1)
				scanner.Split(splitNDJSON)
				if got := scanner.Scan(); got != (n <= tc.limit) {
					t.Fatalf("accepted=%v size=%d limit=%d", got, n, tc.limit)
				}
			})
		}
	}
}

func TestCallerRootIsSyntheticAndSessionDoesNotPersist(t *testing.T) {
	root := RootCaller(0)
	if root.Role != "owner" || root.Actor != "root@local" || root.MFAat == 0 || len(root.Scopes) != 1 || root.Scopes[0] != "*" {
		t.Fatalf("root %+v", root)
	}
	web := RootCaller(1001)
	if web.Role != "" || web.MFAat != 0 || len(web.Scopes) != 0 {
		t.Fatalf("web gained authority %+v", web)
	}
}

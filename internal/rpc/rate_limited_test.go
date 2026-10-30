package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"

	"novapanel/internal/agent"
)

func TestRateLimitedCatalogDispatchAndClient(t *testing.T) {
	s := NewServer(agent.New())
	s.Registry = map[string]Verb{"quota": {Name: "quota", Public: true, ReadOnly: true, NewArgs: func() any { return &struct{}{} }, Run: func(context.Context, *agent.Agent, any) (any, error) { return nil, Errf("rate_limited", "Retry later") }}}
	req := Request{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Verb: "quota", Args: json.RawMessage(`{}`)}
	reply := s.Dispatch(context.Background(), req)
	if reply.Code != "rate_limited" {
		t.Fatalf("dispatch: %+v", reply)
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go func() {
		defer right.Close()
		var r Request
		if json.NewDecoder(right).Decode(&r) == nil {
			reply.ID = r.ID
			Encode(right, reply)
		}
	}()
	scanner := bufio.NewScanner(left)
	scanner.Buffer(make([]byte, 4096), MaxReplyBytes)
	scanner.Split(splitNDJSON)
	c := &Client{conn: left, scanner: scanner}
	got, err := c.Call(context.Background(), "quota", struct{}{}, "")
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != "rate_limited" || got.Code != "rate_limited" {
		t.Fatalf("client: %+v %v", got, err)
	}
}

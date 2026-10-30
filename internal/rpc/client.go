package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Client struct {
	conn    net.Conn
	scanner *bufio.Scanner
	mu      sync.Mutex
}

var requestCounter atomic.Uint64

func Dial(socket string) (*Client, error) {
	c, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	s := bufio.NewScanner(c)
	s.Buffer(make([]byte, 4096), MaxReplyBytes)
	s.Split(splitNDJSON)
	return &Client{conn: c, scanner: s}, nil
}
func (c *Client) Close() error { return c.conn.Close() }
func (c *Client) Call(ctx context.Context, verb string, args any, session string) (Reply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var reply Reply
	if err := ctx.Err(); err != nil {
		return reply, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return reply, err
	}
	if !object(raw) {
		return reply, fmt.Errorf("object args required")
	}
	req := Request{ID: strconv.FormatUint(requestCounter.Add(1), 10), Verb: verb, Args: raw, Session: session}
	if deadline, ok := ctx.Deadline(); ok {
		ms := time.Until(deadline).Milliseconds()
		if ms < 1 {
			ms = 1
		}
		req.DeadlineMs = int(ms)
		_ = c.conn.SetDeadline(deadline)
	} else {
		_ = c.conn.SetDeadline(time.Time{})
	}
	b, err := json.Marshal(req)
	if err != nil {
		return reply, err
	}
	if len(b) > MaxRequestBytes {
		return reply, fmt.Errorf("request too large")
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = c.conn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	defer func() { close(done); <-stopped }()
	if _, err = c.conn.Write(append(b, '\n')); err != nil {
		_ = c.Close()
		return reply, err
	}
	if !c.scanner.Scan() {
		err = c.scanner.Err()
		if err == nil {
			err = fmt.Errorf("connection closed")
		}
		_ = c.Close()
		return reply, err
	}
	line := c.scanner.Bytes()
	if len(line)+1 > MaxReplyBytes || !object(line) {
		_ = c.Close()
		return reply, fmt.Errorf("invalid reply frame")
	}
	if err = json.Unmarshal(line, &reply); err != nil || reply.ID != req.ID || (!reply.OK && !validCode(reply.Code)) || (reply.OK && reply.Code != "") {
		_ = c.Close()
		return Reply{}, fmt.Errorf("invalid reply envelope")
	}
	if !reply.OK {
		return reply, Errf(reply.Code, "%s", reply.Error)
	}
	return reply, nil
}

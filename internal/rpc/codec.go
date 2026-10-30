package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
)

const MaxRequestBytes = 1 << 20
const MaxReplyBytes = 4 << 20

type Request struct {
	ID             string          `json:"id"`
	Verb           string          `json:"verb"`
	Args           json.RawMessage `json:"args"`
	Actor          string          `json:"actor,omitempty"`
	Session        string          `json:"session,omitempty"`
	DeadlineMs     int             `json:"deadline_ms,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}
type Reply struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

func OK(data any) Reply          { return Reply{OK: true, Data: data} }
func Err(code, msg string) Reply { return Reply{Code: code, Error: msg} }
func object(b []byte) bool       { b = bytes.TrimSpace(b); return len(b) > 1 && b[0] == '{' && json.Valid(b) }
func DecodeLine(line []byte) (Request, error) {
	var req Request
	if len(line) > MaxRequestBytes || !object(line) {
		return req, errors.New("invalid request frame")
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return req, err
	}
	if req.Verb == "" || !object(req.Args) {
		return req, errors.New("verb and object args required")
	}
	return req, nil
}
func Encode(w io.Writer, reply Reply) error {
	b, err := json.Marshal(reply)
	if err != nil || len(b)+1 > MaxReplyBytes {
		log.Print("rpc: reply encoding failed or exceeded size limit")
		id := reply.ID
		reply = Err("agent_error", "Internal agent error")
		reply.ID = boundedID(id)
		b, _ = json.Marshal(reply)
		if len(b)+1 > MaxReplyBytes {
			reply.ID = ""
			b, _ = json.Marshal(reply)
		}
	}
	b = append(b, '\n')
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}

// splitNDJSON never emits an unterminated final frame, unlike bufio.ScanLines.
func splitNDJSON(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, errors.New("unterminated NDJSON frame")
	}
	return 0, nil, nil
}

func boundedID(id string) string {
	if len(id) > MaxRequestBytes {
		return ""
	}
	return id
}

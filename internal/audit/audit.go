// Package audit writes durable redacted action records to two independent sinks.
// A file append and SQLite commit cannot be atomic: a DB failure can leave a
// file-only event. Every failure latches unhealthy and must block further effects.
package audit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"novapanel/internal/id"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"os"
	"sync"
	"time"
)

// Principal contains resolved identifiers only, never transport credentials or hashes.
type Principal struct {
	Kind      string `json:"kind"`
	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	TokenID   string `json:"token_id,omitempty"`
}

// JobPrincipal projects only the authority preserved by the existing job schema.
func JobPrincipal(createdBy, role string, scopes []string) Principal {
	if createdBy == "local-root" {
		return Principal{Kind: "local-root"}
	}
	if createdBy == "" {
		return Principal{Kind: "unresolved"}
	}
	if role == "token" || (role == "" && len(scopes) > 0) {
		return Principal{Kind: "token", TokenID: createdBy}
	}
	return Principal{Kind: "user", SessionID: createdBy}
}

type Event struct {
	Principal                                   Principal
	Actor, Role, Verb, ObjectKey, Outcome, Code string
	Detail                                      map[string]any
}

func (ev Event) validate() error {
	if ev.Outcome != "ok" && ev.Outcome != "refused" && ev.Outcome != "error" {
		return errors.New("invalid audit outcome")
	}
	if stage, ok := ev.Detail["stage"]; ok {
		switch stage {
		case "authz", "resolve", "enqueue", "exec", "claim":
		default:
			return errors.New("invalid audit stage")
		}
	}
	return nil
}

type Sink interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}
type Log struct {
	mu     sync.Mutex
	s      *store.Store
	sink   Sink
	err    error
	closed bool
}

func New(s *store.Store, sink Sink) *Log {
	l := &Log{s: s, sink: sink}
	if s == nil || sink == nil {
		l.err = errors.New("audit sinks unavailable")
	}
	return l
}

// Open never executes host commands. The agent must establish the filesystem
// append-only flag through its injected runner before accepting requests.
func Open(s *store.Store, path string) (*Log, error) {
	if s == nil {
		return nil, errors.New("audit store unavailable")
	}
	fd, e := unix.Open(path, unix.O_RDWR|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e == nil && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1) {
		e = errors.New("unsafe audit file")
	}
	if e == nil {
		e = f.Sync()
	}
	if e != nil {
		f.Close()
		return nil, e
	}
	l := New(s, f)
	if e = l.replay(f); e != nil {
		f.Close()
		return nil, e
	}
	return l, nil
}
func (l *Log) Healthy() error {
	if l == nil {
		return errors.New("audit unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}
func (l *Log) Record(ev Event) error { return l.record("", ev) }

// RecordWithID records an agent-allocated ULID; retries retain the first timestamp.
// Any failure remains sticky until the log is reopened and reconciled.
func (l *Log) RecordWithID(eid string, ev Event) error {
	if eid == "" {
		eid = "invalid"
	}
	return l.record(eid, ev)
}

func (l *Log) record(eid string, ev Event) error {
	if l == nil {
		return errors.New("audit unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	fail := func(e error) error { l.err = e; return e }
	if e := ev.validate(); e != nil {
		return fail(e)
	}
	stable := eid != ""
	if stable && !id.Valid(eid) {
		return fail(errors.New("invalid audit event ID"))
	}
	var e error
	if !stable {
		eid, e = id.New()
		if e != nil {
			return fail(e)
		}
	}
	redacted := secret.Redact(ev.Detail)
	if ev.Principal.Kind != "" {
		// This typed, trusted projection bypasses key-name redaction for token_id
		// only. Values still pass free-text secret redaction; arbitrary detail does not.
		p := ev.Principal
		p.Kind = secret.RedactText(p.Kind)
		p.UserID = secret.RedactText(p.UserID)
		p.SessionID = secret.RedactText(p.SessionID)
		p.TokenID = secret.RedactText(p.TokenID)
		if redacted == nil {
			redacted = map[string]any{}
		}
		redacted["principal"] = p
	}
	detail, e := json.Marshal(redacted)
	if e != nil {
		return fail(e)
	}
	v := store.Event{ID: eid, TS: time.Now().UnixMilli(), Actor: secret.RedactText(ev.Actor), Role: secret.RedactText(ev.Role), Verb: secret.RedactText(ev.Verb), ObjectKey: secret.RedactText(ev.ObjectKey), Outcome: ev.Outcome, Code: secret.RedactText(ev.Code), Detail: detail}
	if stable {
		f, ok := l.sink.(interface {
			io.ReaderAt
			Stat() (os.FileInfo, error)
		})
		if !ok {
			return fail(errors.New("stable audit recording requires readable file"))
		}
		old, found, err := findEvent(f, eid)
		if err != nil {
			return fail(err)
		}
		if found {
			v.TS = old.TS
			if !sameEvent(old, v) {
				return fail(errors.New("audit event ID conflict"))
			}
			if err = l.s.ReplayEvent(context.Background(), old); err != nil {
				return fail(err)
			}
			return nil
		}
	}
	// Use explicit snake_case keys, sharing the same ID and data across both sinks.
	b, e := json.Marshal(map[string]any{"id": v.ID, "ts": v.TS, "actor": v.Actor, "role": v.Role, "verb": v.Verb, "object_key": v.ObjectKey, "outcome": v.Outcome, "code": v.Code, "detail": json.RawMessage(detail)})
	if e != nil {
		return fail(e)
	}
	b = append(b, '\n')
	n, e := l.sink.Write(b)
	if e != nil {
		return fail(e)
	}
	if n != len(b) {
		return fail(io.ErrShortWrite)
	}
	if e = l.sink.Sync(); e != nil {
		return fail(e)
	}
	if e = l.s.InsertEvent(context.Background(), v); e != nil {
		return fail(e)
	}
	return nil
}
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	l.err = errors.New("audit closed")
	if l.sink == nil {
		return nil
	}
	return l.sink.Close()
}

// replay reads the append-only source once before request admission. Partial or
// malformed lines fail startup rather than discarding evidence.
func (l *Log) replay(f *os.File) error {
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return e
	}
	r := bufio.NewReader(f)
	for {
		line, e := r.ReadBytes('\n')
		if e == io.EOF && len(line) == 0 {
			return nil
		}
		if e != nil {
			return e
		}
		v, err := decodeEvent(line)
		if err != nil {
			return err
		}
		if err = l.s.ReplayEvent(context.Background(), v); err != nil {
			return err
		}
	}
}

func decodeEvent(line []byte) (store.Event, error) {
	var e error
	var v struct {
		ID        string          `json:"id"`
		TS        *int64          `json:"ts"`
		Actor     *string         `json:"actor"`
		Role      string          `json:"role"`
		Verb      *string         `json:"verb"`
		ObjectKey string          `json:"object_key"`
		Outcome   string          `json:"outcome"`
		Code      string          `json:"code"`
		Detail    json.RawMessage `json:"detail"`
	}
	if e = json.Unmarshal(line, &v); e != nil {
		return store.Event{}, e
	}
	if !id.Valid(v.ID) || v.TS == nil || v.Actor == nil || v.Verb == nil {
		return store.Event{}, errors.New("invalid audit record")
	}
	var detail map[string]any
	if len(v.Detail) != 0 {
		if e = json.Unmarshal(v.Detail, &detail); e != nil {
			return store.Event{}, e
		}
	}
	if e = (Event{Outcome: v.Outcome, Detail: detail}).validate(); e != nil {
		return store.Event{}, e
	}
	return store.Event{ID: v.ID, TS: *v.TS, Actor: *v.Actor, Role: v.Role, Verb: *v.Verb, ObjectKey: v.ObjectKey, Outcome: v.Outcome, Code: v.Code, Detail: v.Detail}, nil
}

// Scan the source of truth without a growing ID cache or changing its offset.
func findEvent(f interface {
	io.ReaderAt
	Stat() (os.FileInfo, error)
}, eid string) (store.Event, bool, error) {
	st, err := f.Stat()
	if err != nil {
		return store.Event{}, false, err
	}
	r := bufio.NewReader(io.NewSectionReader(f, 0, st.Size()))
	var found store.Event
	seen := false
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF && len(line) == 0 {
			return found, seen, nil
		}
		if err != nil {
			return store.Event{}, false, err
		}
		v, err := decodeEvent(line)
		if err != nil {
			return store.Event{}, false, err
		}
		if v.ID == eid {
			if seen && (found.TS != v.TS || !sameEvent(found, v)) {
				return store.Event{}, false, errors.New("audit event ID conflict")
			}
			found, seen = v, true
		}
	}
}

func sameEvent(a, b store.Event) bool {
	return a.ID == b.ID && a.Actor == b.Actor && a.Role == b.Role && a.Verb == b.Verb && a.ObjectKey == b.ObjectKey && a.Outcome == b.Outcome && a.Code == b.Code && bytes.Equal(canonicalDetail(a.Detail), canonicalDetail(b.Detail))
}

func canonicalDetail(raw json.RawMessage) []byte {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

// Package audit writes durable redacted action records to two independent sinks.
// A file append and SQLite commit cannot be atomic: a DB failure can leave a
// file-only event. Every failure latches unhealthy and must block further effects.
package audit

import (
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

type Event struct {
	Actor, Role, Verb, ObjectKey, Outcome, Code string
	Detail                                      map[string]any
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
	fd, e := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
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
	return New(s, f), nil
}
func (l *Log) Healthy() error {
	if l == nil {
		return errors.New("audit unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}
func (l *Log) Record(ev Event) error {
	if l == nil {
		return errors.New("audit unavailable")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return l.err
	}
	fail := func(e error) error { l.err = e; return e }
	if ev.Outcome != "ok" && ev.Outcome != "refused" && ev.Outcome != "error" {
		return fail(errors.New("invalid audit outcome"))
	}
	eid, e := id.New()
	if e != nil {
		return fail(e)
	}
	detail, e := json.Marshal(secret.Redact(ev.Detail))
	if e != nil {
		return fail(e)
	}
	v := store.Event{ID: eid, TS: time.Now().UnixMilli(), Actor: secret.RedactText(ev.Actor), Role: secret.RedactText(ev.Role), Verb: secret.RedactText(ev.Verb), ObjectKey: secret.RedactText(ev.ObjectKey), Outcome: ev.Outcome, Code: secret.RedactText(ev.Code), Detail: detail}
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

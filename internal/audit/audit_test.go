package audit

import (
	"bytes"
	"context"
	"errors"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

type fakeSink struct {
	bytes.Buffer
	fail     bool
	short    bool
	syncFail bool
}

func (f *fakeSink) Write(b []byte) (int, error) {
	if f.fail {
		return 0, errors.New("disk full")
	}
	if f.short {
		return len(b) - 1, nil
	}
	return f.Buffer.Write(b)
}
func (f *fakeSink) Sync() error {
	if f.syncFail {
		return errors.New("sync failed")
	}
	return nil
}
func (f *fakeSink) Close() error { return nil }
func TestDualSinkRedactedAndFailClosed(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	sink := &fakeSink{}
	l := New(s, sink)
	for _, outcome := range []string{"ok", "refused", "error"} {
		if e = l.Record(Event{Actor: "root", Verb: "test", Outcome: outcome, Detail: map[string]any{"password": "shh"}}); e != nil {
			t.Fatal(e)
		}
	}
	ev, e := s.ListEvents(context.Background(), 10, "")
	if e != nil || len(ev) != 3 || bytes.Contains(sink.Bytes(), []byte("shh")) {
		t.Fatal(ev, e, sink.String())
	}
	sink.fail = true
	if e = l.Record(Event{Verb: "test", Outcome: "ok"}); e == nil {
		t.Fatal("write failure ignored")
	}
	sink.fail = false
	if l.Healthy() == nil || l.Record(Event{Verb: "test", Outcome: "ok"}) == nil {
		t.Fatal("failure did not latch")
	}
	ev, _ = s.ListEvents(context.Background(), 10, "")
	if len(ev) != 3 {
		t.Fatal(ev)
	}
}
func TestAuditFileAndFailureModes(t *testing.T) {
	for _, mode := range []string{"short", "sync", "db"} {
		t.Run(mode, func(t *testing.T) {
			s, e := store.Open(filepath.Join(t.TempDir(), "db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			f := &fakeSink{short: mode == "short", syncFail: mode == "sync"}
			l := New(s, f)
			if mode == "db" {
				s.Close()
			}
			if l.Record(Event{Outcome: "ok"}) == nil || l.Healthy() == nil {
				t.Fatal("ignored failure")
			}
		})
	}
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := filepath.Join(t.TempDir(), "audit.log")
	l, e := Open(s, p)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.Record(Event{Outcome: "ok"}); e != nil {
		t.Fatal(e)
	}
	l.Close()
	if l.Healthy() == nil {
		t.Fatal("closed is healthy")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(p, link)
	if l, e = Open(s, link); e == nil {
		l.Close()
		t.Fatal("symlink accepted")
	}
}

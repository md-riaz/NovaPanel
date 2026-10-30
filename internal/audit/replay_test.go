package audit

import (
	"context"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenReplaysFileOnlyEventsIdempotently(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	path := filepath.Join(dir, "audit.log")
	s, e := store.Open(db)
	if e != nil {
		t.Fatal(e)
	}
	l, e := Open(s, path)
	if e != nil {
		t.Fatal(e)
	}
	s.Close() // Force the file-first/DB-second crash window through the real log.
	if e = l.Record(Event{Actor: "owner", Verb: "service.restart", Outcome: "ok", Detail: map[string]any{"stage": "exec", "job_id": "j"}}); e == nil {
		t.Fatal("DB failure ignored")
	}
	l.Close()
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) == 0 {
		t.Fatal(string(raw), e)
	}
	s, e = store.Open(db)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for i := 0; i < 2; i++ {
		l, e = Open(s, path)
		if e != nil {
			t.Fatal(e)
		}
		l.Close()
		events, e := s.ListEvents(context.Background(), 10, "")
		if e != nil || len(events) != 1 || events[0].Verb != "service.restart" {
			t.Fatal(events, e)
		}
	}
}
func TestAuditRejectsInvalidStage(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	l := New(s, &fakeSink{})
	if e = l.Record(Event{Outcome: "ok", Detail: map[string]any{"stage": "made-up"}}); e == nil {
		t.Fatal("invalid stage accepted")
	}
}

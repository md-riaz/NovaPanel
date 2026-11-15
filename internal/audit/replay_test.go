package audit

import (
	"bytes"
	"context"
	"encoding/json"
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
func TestOpenRejectsMalformedRecords(t *testing.T) {
	base := map[string]any{"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "ts": int64(1), "actor": "root", "role": "owner", "verb": "service.restart", "object_key": "", "outcome": "ok", "code": "", "detail": map[string]any{"stage": "exec"}}
	cases := map[string]func(map[string]any){
		"invalid ID":        func(v map[string]any) { v["id"] = "bad" },
		"invalid outcome":   func(v map[string]any) { v["outcome"] = "success" },
		"invented stage":    func(v map[string]any) { v["detail"] = map[string]any{"stage": "made-up"} },
		"non-string stage":  func(v map[string]any) { v["detail"] = map[string]any{"stage": 1} },
		"null stage":        func(v map[string]any) { v["detail"] = map[string]any{"stage": nil} },
		"non-object detail": func(v map[string]any) { v["detail"] = []any{} },
	}
	for _, field := range []string{"ts", "actor", "verb"} {
		cases["missing "+field] = func(v map[string]any) { delete(v, field) }
		cases["null "+field] = func(v map[string]any) { v[field] = nil }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := make(map[string]any)
			for k, value := range base {
				v[k] = value
			}
			mutate(v)
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			path := filepath.Join(dir, "audit.log")
			if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			if l, err := Open(s, path); err == nil {
				l.Close()
				t.Fatal("malformed audit record accepted at startup")
			}
			events, err := s.ListEvents(context.Background(), 10, "")
			if err != nil || len(events) != 0 {
				t.Fatal(events, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, append(raw, '\n')) {
				t.Fatal("audit evidence changed", err)
			}
		})
	}
}

func TestOpenReplaysHistoricalRecords(t *testing.T) {
	for _, detail := range []string{"", `,"detail":null`, `,"detail":{}`, `,"detail":{"stage":"authz"}`, `,"detail":{"stage":"resolve"}`, `,"detail":{"stage":"enqueue"}`, `,"detail":{"stage":"exec"}`, `,"detail":{"stage":"claim"}`} {
		t.Run(detail, func(t *testing.T) {
			dir := t.TempDir()
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			path := filepath.Join(dir, "audit.log")
			// Empty labels, zero timestamps and absent optional fields were writable.
			raw := []byte(`{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","ts":0,"actor":"","verb":"","outcome":"refused"` + detail + "}\n")
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				l, err := Open(s, path)
				if err != nil {
					t.Fatal(err)
				}
				l.Close()
			}
			events, err := s.ListEvents(context.Background(), 10, "")
			if err != nil || len(events) != 1 || events[0].TS != 0 || events[0].Actor != "" || events[0].Verb != "" {
				t.Fatal(events, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatal("audit evidence changed", err)
			}
		})
	}
}

func TestOpenRejectsConflictingOrPartialRecords(t *testing.T) {
	line := `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","ts":1,"actor":"root","verb":"test","outcome":"ok","detail":null}`
	for name, raw := range map[string]string{
		"partial":  line,
		"conflict": line + "\n" + `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","ts":1,"actor":"root","verb":"test","outcome":"error","detail":null}` + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			path := filepath.Join(dir, "audit.log")
			if err = os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if l, err := Open(s, path); err == nil {
				l.Close()
				t.Fatal("corruption accepted")
			}
		})
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
	if l.Healthy() == nil || l.Record(Event{Outcome: "ok"}) == nil {
		t.Fatal("validation failure did not latch")
	}
}

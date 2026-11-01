package audit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"novapanel/internal/store"
)

func TestHistoricalPublicationReplayAndStableID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	// This is the JSON format written by the previous recorder.
	raw := []byte("{\"actor\":\"owner\",\"code\":\"\",\"detail\":{\"job_id\":\"old-job\",\"publication\":\"not_committed\",\"stage\":\"publication\"},\"id\":\"01ARZ3NDEKTSV4RRFFQ69G5FAV\",\"object_key\":\"db:old\",\"outcome\":\"error\",\"role\":\"owner\",\"ts\":123,\"verb\":\"database.create\"}\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 2; i++ {
		l, err := Open(s, path)
		if err != nil {
			t.Fatal(err)
		}
		if err = l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.ListEvents(context.Background(), 10, "")
	if err != nil || len(events) != 1 {
		t.Fatalf("events: %+v %v", events, err)
	}
	e := events[0]
	if e.ID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" || e.TS != 123 || e.Actor != "owner" || e.Role != "owner" || e.Verb != "database.create" || e.ObjectKey != "db:old" || e.Outcome != "error" || e.Code != "" || string(e.Detail) != `{"job_id":"old-job","publication":"not_committed","stage":"publication"}` {
		t.Fatalf("mirror changed: %+v detail=%s", e, e.Detail)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("file changed: %s %v", got, err)
	}
	l, err := Open(s, path)
	if err != nil {
		t.Fatal(err)
	}
	ev := Event{Actor: "owner", Verb: "test", Outcome: "ok", Detail: map[string]any{"stage": "exec"}}
	const stable = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	if err = l.RecordWithID(stable, ev); err != nil {
		t.Fatal(err)
	}
	once, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.RecordWithID(stable, ev); err != nil {
		t.Fatal(err)
	}
	twice, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(once, twice) || !bytes.HasPrefix(twice, raw) {
		t.Fatalf("stable replay changed file: %v", err)
	}
	events, err = s.ListEvents(context.Background(), 10, "")
	if err != nil || len(events) != 2 {
		t.Fatalf("stable mirror: %+v %v", events, err)
	}
	if err = l.RecordWithID("01ARZ3NDEKTSV4RRFFQ69G5FAX", Event{Outcome: "error", Detail: map[string]any{"stage": "publication"}}); err == nil {
		t.Fatal("new publication stage accepted")
	}
	l.Close()
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, twice) {
		t.Fatal("rejected event changed file", err)
	}
}

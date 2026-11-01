package audit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"novapanel/internal/store"
)

const stableID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func publicationEvent() Event {
	return Event{Principal: Principal{Kind: "token", TokenID: "trusted-token-id"}, Actor: "owner", Role: "owner", Verb: "database.create", ObjectKey: "db:original", Outcome: "ok", Detail: map[string]any{"stage": "exec", "publication": "committed", "job_id": "original", "password": "never-log-this", "count": int64(9007199254740993)}}
}

func TestRecordWithIDReplay(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db")
	path := filepath.Join(dir, "audit.log")
	s, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	l, err := Open(s, path)
	if err != nil {
		t.Fatal(err)
	}
	ev := publicationEvent()
	if err = l.RecordWithID(stableID, ev); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.RecordWithID(stableID, ev); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	l.Close()
	s.Close()
	for i := 0; i < 2; i++ {
		s, err = store.Open(db)
		if err != nil {
			t.Fatal(err)
		}
		l, err = Open(s, path)
		if err != nil {
			t.Fatal(err)
		}
		if err = l.RecordWithID(stableID, ev); err != nil {
			t.Fatal(err)
		}
		events, err := s.ListEvents(context.Background(), 10, "")
		if err != nil || len(events) != 1 || events[0].ID != stableID {
			t.Fatal(events, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("replay appended evidence", err)
		}
		if bytes.Contains(after, []byte("never-log-this")) || !bytes.Contains(after, []byte("trusted-token-id")) {
			t.Fatal("redaction/principal mismatch")
		}
		l.Close()
		s.Close()
	}
}

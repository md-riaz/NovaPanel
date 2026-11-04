package audit

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/store"
	"os"
	"path/filepath"
	"testing"
)

type faultFile struct {
	*os.File
	mode string
}

func (f *faultFile) Write(b []byte) (int, error) {
	switch f.mode {
	case "write":
		return 0, errors.New("injected append failure")
	case "short":
		return f.File.Write(b[:len(b)/2])
	}
	return f.File.Write(b)
}
func (f *faultFile) Sync() error {
	if f.mode == "sync" {
		return errors.New("injected sync failure")
	}
	return f.File.Sync()
}

func TestRecordWithIDFileFaults(t *testing.T) {
	for _, mode := range []string{"write", "short", "sync", "mirror"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "db")
			path := filepath.Join(dir, "audit.log")
			s, err := store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			sink := &faultFile{File: f, mode: mode}
			l := New(s, sink)
			if mode == "mirror" {
				if err = s.Tx(context.Background(), func(tx *sql.Tx) error {
					_, err := tx.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'injected mirror failure'); END`)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			err = l.RecordWithID(stableID, publicationEvent())
			if err == nil {
				t.Fatal("fault accepted")
			}
			t.Logf("%s failure: %v", mode, err)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "write" && len(raw) != 0 {
				t.Fatal("append happened")
			}
			if mode != "write" && len(raw) == 0 {
				t.Fatal("failure occurred before actual append")
			}
			events, err := s.ListEvents(context.Background(), 10, "")
			if err != nil || len(events) != 0 {
				t.Fatal(events, err)
			}
			sink.mode = ""
			if mode == "mirror" {
				if err = s.Tx(context.Background(), func(tx *sql.Tx) error { _, err := tx.Exec(`DROP TRIGGER fail_audit`); return err }); err != nil {
					t.Fatal(err)
				}
			}
			if l.Healthy() == nil || l.RecordWithID(stableID, publicationEvent()) == nil || l.Record(Event{Outcome: "ok"}) == nil {
				t.Fatal("sticky failure cleared")
			}
			l.Close()
			s.Close()
			s, err = store.Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			l, err = Open(s, path)
			if mode == "short" {
				if err == nil {
					l.Close()
					t.Fatal("partial append accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			if err = l.RecordWithID(stableID, publicationEvent()); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "write" && !bytes.Equal(raw, after) {
				t.Fatal("duplicate append after recovery")
			}
			events, err = s.ListEvents(context.Background(), 10, "")
			if err != nil || len(events) != 1 {
				t.Fatal(events, err)
			}
		})
	}
}

func TestRecordWithIDRejectsConflictAndInvalidID(t *testing.T) {
	for _, kind := range []string{"empty", "lowercase", "overflow", "changed", "reopened", "large_number"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s, err := store.Open(filepath.Join(dir, "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			path := filepath.Join(dir, "audit.log")
			l, err := Open(s, path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { l.Close() }()
			ev := publicationEvent()
			eid := stableID
			if kind == "changed" || kind == "reopened" || kind == "large_number" {
				if err = l.RecordWithID(eid, ev); err != nil {
					t.Fatal(err)
				}
				if kind == "reopened" {
					l.Close()
					l, err = Open(s, path)
					if err != nil {
						t.Fatal(err)
					}
				}
				if kind == "large_number" {
					ev.Detail["count"] = int64(9007199254740990)
				} else {
					ev.Detail["job_id"] = "different"
				}
			} else {
				switch kind {
				case "empty":
					eid = ""
				case "lowercase":
					eid = "01arz3ndektsv4rrffq69g5fav"
				case "overflow":
					eid = "81ARZ3NDEKTSV4RRFFQ69G5FAV"
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err = l.RecordWithID(eid, ev); err == nil {
				t.Fatal("invalid identity/event accepted")
			}
			t.Log(err)
			if l.Healthy() == nil || l.RecordWithID(stableID, publicationEvent()) == nil {
				t.Fatal("failure not sticky")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("conflict appended", err)
			}
		})
	}
}

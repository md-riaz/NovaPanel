package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestScheduledAtomicCASBusyAndAuthority(t *testing.T) {
	s := fresh(t)
	ctx := context.Background()
	v := Schedule{ID: "s", Verb: "v", Args: json.RawMessage(`{}`), Cron: "daily", NextRun: 10, Enabled: true, Actor: "trusted", Role: "admin", CreatedBy: "session", Scopes: []string{"sites:write"}, Automation: true}
	if e := s.PutSchedule(ctx, v); e != nil {
		t.Fatal(e)
	}
	j := Job{ID: "j", Verb: "v", Args: v.Args, ObjectKey: "site:a", Actor: "forged", Role: "owner", CreatedBy: "attacker"}
	c := JobClaim{JobID: "j", ClaimHash: "h"}
	ok, e := s.EnqueueScheduledJob(ctx, "s", 10, 20, 11, json.RawMessage(`{"n":1}`), j, &c)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	got, e := s.GetJob(ctx, "j")
	if e != nil || got.Actor != "trusted" || got.Role != "admin" || got.CreatedBy != "session" || !got.Automation || len(got.Scopes) != 1 {
		t.Fatal(got, e)
	}
	j.ID = "duplicate"
	ok, e = s.EnqueueScheduledJob(ctx, "s", 10, 20, 11, json.RawMessage(`{}`), j, nil)
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	if _, e = s.StartJob(ctx, "j", 12); e != nil {
		t.Fatal(e)
	}
	j.ID = "busy"
	ok, e = s.EnqueueScheduledJob(ctx, "s", 20, 30, 21, json.RawMessage(`{"n":2}`), j, nil)
	if e != nil || ok {
		t.Fatal(ok, e)
	}
	v, e = s.GetSchedule(ctx, "s")
	if e != nil || v.NextRun != 30 || v.LastRun == nil || *v.LastRun != 21 || string(v.State) != `{"n":2}` {
		t.Fatal(v, e)
	}
	if _, e = s.GetJob(ctx, "busy"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	// Failed job insertion must roll back the cadence, state and claim reservation.
	j.ID = "j"
	j.ObjectKey = "other"
	ok, e = s.EnqueueScheduledJob(ctx, "s", 30, 40, 31, json.RawMessage(`{}`), j, nil)
	if e == nil || ok {
		t.Fatal(ok, e)
	}
	v, e = s.GetSchedule(ctx, "s")
	if e != nil || v.NextRun != 30 {
		t.Fatal(v, e)
	}
}
func TestCloseCancelsDirectQueryAndOpenRows(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	s, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	// An open Rows owns the sole connection; another direct query waits for it.
	rows, e := s.db.QueryContext(context.Background(), "SELECT 1 UNION ALL SELECT 2")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.db.QueryContext(context.Background(), "SELECT 3"); done <- e }()
	deadline := time.After(time.Second)
	for {
		s.mu.Lock()
		n := len(s.operations)
		s.mu.Unlock()
		if n == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("query not admitted")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("query not canceled")
		}
	default:
		t.Fatal("Close did not await direct query")
	}
	if rows.Next() {
		t.Fatal("rows remain open")
	}
	if _, e = s.db.ExecContext(context.Background(), "SELECT 1"); !errors.Is(e, ErrStoreClosed) {
		t.Fatal(e)
	}
	if _, e = s.db.QueryContext(context.Background(), "SELECT 1"); !errors.Is(e, ErrStoreClosed) {
		t.Fatal(e)
	}
	if e = s.Tx(context.Background(), func(*sql.Tx) error { return nil }); !errors.Is(e, ErrStoreClosed) {
		t.Fatal(e)
	}
	other, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	other.Close()
}
func TestBlockedClaimReadEvaluatesClockAfterAcquiringConnection(t *testing.T) {
	c := &testClock{}
	c.now.Store(1)
	s, e := OpenWithClock(filepath.Join(t.TempDir(), "db"), c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	if e = s.CreateJobWithClaim(ctx, Job{ID: "j", Verb: "v", CreatedBy: "owner", Args: json.RawMessage(`{}`)}, JobClaim{JobID: "j", ClaimHash: "h"}); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishClaim(ctx, "j", []byte("n"), []byte("c")); e != nil {
		t.Fatal(e)
	}
	conn, e := s.db.raw.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.ConsumeClaim(ctx, "j", "h", "owner"); done <- e }()
	c.now.Store(ClaimTTLMillis + 1)
	conn.Close()
	if e = <-done; !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	var n int
	if e = s.db.QueryRow("SELECT count(*) FROM job_claims").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestSystemSeededScheduleNullableAuthority(t *testing.T) {
	s := fresh(t)
	if _, e := s.db.ExecContext(context.Background(), "INSERT INTO schedules(id,verb,args_json,cron,next_run) VALUES('seed','update.check','{}','daily',1)"); e != nil {
		t.Fatal(e)
	}
	v, e := s.GetSchedule(context.Background(), "seed")
	if e != nil || v.CreatedBy != "" || v.Role != "" {
		t.Fatal(v, e)
	}
}

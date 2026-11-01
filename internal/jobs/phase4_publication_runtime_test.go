package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/secret"
	"path/filepath"
	"testing"

	"novapanel/internal/audit"
	"novapanel/internal/store"
)

// This protocol fixture mirrors the declared audit dependency into real SQLite.
// It is not acceptance evidence for the append-only audit implementation.
type publicationAuditFixture struct {
	*fakeAudit
	s      *store.Store
	before func(string, audit.Event) error
}

func (a *publicationAuditFixture) RecordWithID(eid string, ev audit.Event) error {
	if a.before != nil {
		if err := a.before(eid, ev); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(ev.Detail)
	if err != nil {
		return err
	}
	return a.s.InsertEvent(context.Background(), store.Event{ID: eid, Actor: ev.Actor, Role: ev.Role, Verb: ev.Verb, ObjectKey: ev.ObjectKey, Outcome: ev.Outcome, Code: ev.Code, Detail: raw})
}

func TestQueueRunPublicationRollbackMatrix(t *testing.T) {
	for _, mode := range []string{"expiry_in_intent", "expiry_waiting_sql", "terminal_trigger", "response_lost"} {
		t.Run(mode, func(t *testing.T) {
			clock := &reviewAtomicClock{}
			clock.n.Store(1000)
			path := filepath.Join(t.TempDir(), "queue.db")
			s, e := store.OpenWithClock(path, clock)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { s.Close() }()
			seal, _ := secret.New(make([]byte, 32))
			a := &publicationAuditFixture{fakeAudit: &fakeAudit{}, s: s}
			q := NewQueue(s, seal, a)
			q.SetClock(clock)
			ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
			if e = q.PrepareOperations(ctx); e != nil {
				t.Fatal(e)
			}
			r, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "original-secret"})
			if e != nil {
				t.Fatal(e)
			}
			op, _ := s.LoadOperation(ctx, r.Job.ID)
			var records []string
			a.before = func(_ string, ev audit.Event) error {
				publication, _ := ev.Detail["publication"].(string)
				if ev.Detail["stage"] != "exec" || publication == "" {
					t.Errorf("publication must be distinguished within F11 exec: %+v", ev)
				}
				records = append(records, publication)
				if mode == "expiry_in_intent" && publication == "intent" {
					clock.n.Store(op.Expires)
				}
				return nil
			}
			if mode == "expiry_waiting_sql" || mode == "response_lost" {
				q.store = &publicationFaultStore{Store: s, clock: clock, deadline: op.Expires, mode: mode}
			}
			q.SetRunner(func(c context.Context, j *Job) error {
				if e := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
					return e
				}
				if e := s.CheckpointOperation(c, j.ID, "db_applied", "verified"); e != nil {
					return e
				}
				if mode == "terminal_trigger" {
					reviewSQL(t, s, `CREATE TRIGGER reject_publication BEFORE UPDATE OF checkpoint ON phase4_operations WHEN NEW.checkpoint='done' BEGIN SELECT RAISE(ABORT,'injected'); END`)
				}
				return nil
			})
			// All injected paths stop Run themselves rather than depend on a polling race.
			e = q.Run(ctx)
			if e == nil {
				t.Fatal("fault swallowed")
			}
			after, err := s.LoadOperation(ctx, r.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			committed := mode == "response_lost"
			if (after.Checkpoint == "done") != committed {
				t.Fatalf("checkpoint=%s", after.Checkpoint)
			}
			var dbs, claims int
			if e = s.Tx(ctx, func(tx *sql.Tx) error {
				if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM databases").Scan(&dbs); e != nil {
					return e
				}
				return tx.QueryRowContext(ctx, "SELECT count(*) FROM job_claims WHERE job_id=? AND length(ciphertext)>0", r.Job.ID).Scan(&claims)
			}); e != nil {
				t.Fatal(e)
			}
			if committed {
				if dbs != 1 || claims != 1 {
					t.Fatalf("lost commit: db=%d claim=%d", dbs, claims)
				}
			} else if dbs != 0 || claims != 0 {
				t.Fatalf("partial publication: db=%d claim=%d", dbs, claims)
			}
			want := "not_committed"
			if committed {
				want = "committed"
			}
			if committed {
				if len(records) != 1 || records[0] != "intent" {
					t.Fatalf("uncertain storage wrote final: %v", records)
				}
			} else if len(records) != 2 || records[0] != "intent" || records[1] != want {
				t.Fatalf("audit stages=%v", records)
			}
			if mode == "terminal_trigger" || committed {
				if q.healthy() == nil {
					t.Fatal("storage failure not sticky")
				}
			}
		})
	}
}

type publicationFaultStore struct {
	*store.Store
	clock    *reviewAtomicClock
	deadline int64
	mode     string
}

func (s *publicationFaultStore) CompleteOperation(ctx context.Context, jid string, claim *store.JobClaim) error {
	if s.mode == "expiry_waiting_sql" {
		// Hold the only pooled SQL connection until the publication clock expires.
		held := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() { done <- s.Store.Tx(ctx, func(*sql.Tx) error { close(held); <-release; return nil }) }()
		<-held
		waiting := make(chan error, 1)
		go func() { waiting <- s.Store.CompleteOperation(ctx, jid, claim) }()
		s.clock.n.Store(s.deadline)
		close(release)
		if err := <-done; err != nil {
			return err
		}
		return <-waiting
	}
	if err := s.Store.CompleteOperation(ctx, jid, claim); err != nil {
		return err
	}
	return errors.New("commit response lost")
}

func TestQueueRunPublicationDoneBeforeFinalAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	a := &fakeAudit{}
	q := NewQueue(s, seal, a)
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	stable := &publicationAuditFixture{fakeAudit: a, s: s}
	q.audit = stable
	if err := q.PrepareOperations(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "original-secret"})
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("final sink failed")
	var intent, final string
	stable.before = func(eid string, ev audit.Event) error {
		switch ev.Detail["publication"] {
		case "intent":
			intent = eid
		case "committed":
			final = eid
			op, e := s.LoadOperation(ctx, r.Job.ID)
			if e != nil || op.Checkpoint != "done" || len(op.Ciphertext) != 0 {
				t.Errorf("final preceded commit: %+v %v", op, e)
			}
			if _, e = q.Status(ctx, r.Job.ID); !publicationAgentError(e) {
				t.Errorf("pending status: %v", e)
			}
			if _, e = q.Claim(ctx, r.Job.ID, r.ClaimToken); !publicationAgentError(e) {
				t.Errorf("pending claim: %v", e)
			}
			return sentinel
		}
		return nil
	}
	q.SetRunner(func(c context.Context, j *Job) error {
		if e := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
			return e
		}
		return s.CheckpointOperation(c, j.ID, "db_applied", "verified")
	})
	if err = q.Run(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("Run=%v", err)
	}
	if intent == "" || final == "" || intent == final {
		t.Fatal("stable audit IDs missing")
	}
	if _, err = q.Enqueue(ctx, "reconcile", map[string]any{}, "system"); err == nil {
		t.Fatal("storage/audit failure was not sticky")
	}
	// Recovery must finalize the same event without rerunning effects or resealing.
	stable.before = func(eid string, ev audit.Event) error {
		if ev.Detail["publication"] != "committed" || eid != final {
			t.Errorf("recovery changed event: %s %+v", eid, ev)
		}
		return nil
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	stable.s = s
	reopened := NewQueue(s, q.sealer, stable)
	reopened.SetRunner(func(context.Context, *Job) error { t.Error("committed effects replayed"); return nil })
	if err = reopened.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	if j, e := reopened.Status(ctx, r.Job.ID); e != nil || j.State != "done" {
		t.Fatalf("acknowledged status: %+v %v", j, e)
	}
	claim, e := reopened.Claim(ctx, r.Job.ID, r.ClaimToken)
	if e != nil || claim["password"] != "original-secret" {
		t.Fatalf("original claim lost: %v %v", claim, e)
	}
}

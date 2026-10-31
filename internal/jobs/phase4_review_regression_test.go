package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type reviewAtomicClock struct{ n atomic.Int64 }

func (c *reviewAtomicClock) Now() int64 { return c.n.Load() }

type reviewDeadlineStore struct {
	*store.Store
	clock    *reviewAtomicClock
	deadline int64
	crossed  bool
}

func (s *reviewDeadlineStore) PendingOperations(ctx context.Context) ([]store.Job, error) {
	if s.deadline > 0 && !s.crossed {
		s.crossed = true
		s.clock.n.Store(s.deadline)
	}
	return s.Store.PendingOperations(ctx)
}
func reviewSQL(t *testing.T, s *store.Store, query string, args ...any) {
	t.Helper()
	if e := s.Tx(context.Background(), func(tx *sql.Tx) error { _, e := tx.ExecContext(context.Background(), query, args...); return e }); e != nil {
		t.Fatal(e)
	}
}
func reviewRunTerminal(t *testing.T, q *Queue, s *store.Store, id string) (store.Job, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx) }()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		j, e := s.GetJob(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "done" || j.State == "failed" {
			cancel()
			return j, <-done
		}
		select {
		case e := <-done:
			latest, readErr := s.GetJob(context.Background(), id)
			if readErr != nil {
				t.Fatal(readErr)
			}
			return latest, e
		case <-timer.C:
			t.Fatal("queue timeout")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestReviewRunDeadlineAfterStartup(t *testing.T) {
	c := &reviewAtomicClock{}
	c.n.Store(1000)
	s, e := store.OpenWithClock(filepath.Join(t.TempDir(), "queue.db"), c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	seal, _ := secret.New(make([]byte, 32))
	wrapped := &reviewDeadlineStore{Store: s, clock: c}
	q := NewQueue(wrapped, seal, &fakeAudit{})
	q.SetClock(c)
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
	if e = q.PrepareOperations(ctx); e != nil {
		t.Fatal(e)
	}
	r, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "deadline-secret"})
	if e != nil {
		t.Fatal(e)
	}
	op, _ := s.LoadOperation(ctx, r.Job.ID)
	wrapped.deadline = op.Expires
	calls := 0
	q.SetRunner(func(context.Context, *Job) error { calls++; return nil })
	j, e := reviewRunTerminal(t, q, s, r.Job.ID)
	if e != nil && !errors.Is(e, ErrRecoveryBlocked) {
		t.Fatal(e)
	}
	op, _ = s.LoadOperation(ctx, r.Job.ID)
	var claims int
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM job_claims WHERE job_id=?", r.Job.ID).Scan(&claims)
	}); err != nil {
		t.Fatal(err)
	}
	if claims != 0 {
		t.Fatal("expired claim reservation retained")
	}
	t.Logf("exact deadline after startup: state=%s code=%s checkpoint=%s ciphertext=%d commands=%d", j.State, j.Code, op.Checkpoint, len(op.Ciphertext), calls)
	if calls != 0 || j.Code != "interrupted" || op.Checkpoint != "expired" || len(op.Ciphertext) != 0 {
		t.Fatal("expired recovery must fail interrupted, clear recovery input and run no commands")
	}
}
func TestReviewRunChangedPreconditions(t *testing.T) {
	for _, change := range []string{"site_failed", "binding_added", "account_added", "target_removed", "primary_changed", "site_rebound", "create_site_failed", "create_publication_site_failed", "dbuser_site_failed", "rotate_site_failed", "empty_accounts"} {
		t.Run(change, func(t *testing.T) {
			s, e := store.Open(filepath.Join(t.TempDir(), "queue.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "local-root", Role: "owner"})
			site := "01K8WMXZF57CGNZKX1FZ38DZMP"
			db := "01K8WMXZF57CGNZKX1FZ38DZMQ"
			primary := "01K8WMXZF57CGNZKX1FZ38DZMR"
			target := "01K8WMXZF57CGNZKX1FZ38DZMS"
			reviewSQL(t, s, `INSERT INTO sites(id,name,type,docroot,status,created,updated) VALUES(?,'review','wordpress','/srv/sites/review/public','active',1,1)`, site)
			if e = s.CreateDatabasePrimary(ctx, store.DatabasePrimary{ID: db, UserID: primary, SiteID: site}); e != nil {
				t.Fatal(e)
			}
			if change == "target_removed" || change == "primary_changed" {
				reviewSQL(t, s, `INSERT INTO db_users(id,db_id,username,host,created) VALUES(?, ?, ?, '127.0.0.1',1)`, target, db, "u_"+strings.ToLower(target))
			}
			seal, _ := secret.New(make([]byte, 32))
			q := NewQueue(s, seal, &fakeAudit{})
			request := store.OperationRequest{Kind: store.DatabaseDelete, DatabaseID: db}
			var fields map[string]any
			if change == "target_removed" || change == "primary_changed" {
				request.Kind = store.DBUserDelete
				request.UserID = target
			}
			if change == "create_site_failed" || change == "create_publication_site_failed" {
				request = store.OperationRequest{Kind: store.DatabaseCreate, SiteID: site}
				fields = map[string]any{"password": "private"}
			}
			if change == "dbuser_site_failed" || change == "rotate_site_failed" {
				request.Kind = store.DBUserCreate
				if change == "rotate_site_failed" {
					request.Kind = store.DatabaseRotate
				}
				fields = map[string]any{"password": "private"}
			}
			if e = q.PrepareOperations(ctx); e != nil {
				t.Fatal(e)
			}
			r, e := q.EnqueueOperation(ctx, request, fields)
			if e != nil {
				t.Fatal(e)
			}
			switch change {
			case "site_failed", "create_site_failed", "dbuser_site_failed", "rotate_site_failed":
				reviewSQL(t, s, `UPDATE sites SET status='failed' WHERE id=?`, site)
			case "binding_added":
				reviewSQL(t, s, `INSERT INTO wordpress_bindings(site_id,db_id) VALUES(?,?)`, site, db)
			case "account_added":
				reviewSQL(t, s, `INSERT INTO db_users(id,db_id,username,host,created) VALUES(?, ?, ?, '127.0.0.1',1)`, target, db, "u_"+strings.ToLower(target))
			case "target_removed":
				reviewSQL(t, s, `DELETE FROM db_users WHERE id=?`, target)
			case "primary_changed":
				reviewSQL(t, s, `UPDATE databases SET primary_user_id=? WHERE id=?`, target, db)
			case "site_rebound":
				reviewSQL(t, s, `UPDATE databases SET site_id=NULL WHERE id=?`, db)
			case "empty_accounts":
				reviewSQL(t, s, `UPDATE phase4_operations SET identities_json=json_set(identities_json,'$.Accounts',json('[]')) WHERE job_id=?`, r.Job.ID)
			}
			calls := 0
			q.SetRunner(func(c context.Context, j *Job) error {
				calls++
				if change == "create_publication_site_failed" {
					if err := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); err != nil {
						return err
					}
					if err := s.CheckpointOperation(c, j.ID, "db_applied", "verified"); err != nil {
						return err
					}
					reviewSQL(t, s, `UPDATE sites SET status='failed' WHERE id=?`, site)
				}
				return nil
			})
			j, e := reviewRunTerminal(t, q, s, r.Job.ID)
			if change == "create_publication_site_failed" {
				if _, err := s.GetDatabase(ctx, j.ObjectKey[3:]); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("ineligible create published", err)
				}
				if _, err := q.Claim(ctx, j.ID, r.ClaimToken); err == nil {
					t.Fatal("ineligible create published claim")
				}
				return
			}
			t.Logf("change=%s commands=%d job=%s code=%s queue_error=%v", change, calls, j.State, j.Code, e)
			if calls != 0 {
				t.Fatal("queue invoked external runner after precondition/identity change")
			}
			if change == "empty_accounts" {
				if e == nil {
					t.Fatal("corrupt identity not rejected")
				}
			} else if e != nil && !errors.Is(e, ErrRecoveryBlocked) {
				t.Fatal(e)
			}
			if change != "empty_accounts" {
				want := "conflict"
				if change == "binding_added" {
					want = "precondition_failed"
				}
				if j.Code != want {
					t.Fatalf("code=%s want %s", j.Code, want)
				}
			}
		})
	}
}
func TestReviewRunAtomicRollbackReopenNoDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	seal, _ := secret.New(make([]byte, 32))
	ctx := WithMetadata(context.Background(), Metadata{CreatedBy: "session-owner", Role: "owner"})
	q := NewQueue(s, seal, &fakeAudit{})
	if e = q.PrepareOperations(ctx); e != nil {
		t.Fatal(e)
	}
	r, e := q.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DatabaseCreate}, map[string]any{"password": "exact-password"})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := s.LoadOperation(ctx, r.Job.ID)
	reviewSQL(t, s, `CREATE TRIGGER review_atomic_abort BEFORE UPDATE OF checkpoint ON phase4_operations WHEN NEW.checkpoint='done' BEGIN SELECT RAISE(ABORT,'independent terminal abort'); END`)
	commands := 0
	q.SetRunner(func(c context.Context, j *Job) error {
		commands++
		if e := s.CheckpointOperation(c, j.ID, "prepared", "db_applied"); e != nil {
			return e
		}
		return s.CheckpointOperation(c, j.ID, "db_applied", "verified")
	})
	runErr := q.Run(ctx)
	if runErr == nil {
		t.Fatal("ignored injected terminal failure")
	}
	op, e := s.LoadOperation(ctx, r.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := s.GetJob(ctx, r.Job.ID)
	if j.State != "running" || op.Checkpoint != "verified" || !reflect.DeepEqual(before.Ciphertext, op.Ciphertext) {
		t.Fatal("partial terminal commit")
	}
	if _, e = s.GetDatabase(ctx, op.DatabaseID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("metadata escaped aborted transaction", e)
	}
	if _, e = q.Claim(ctx, j.ID, r.ClaimToken); e == nil {
		t.Fatal("claim escaped aborted transaction")
	}
	reviewSQL(t, s, `DROP TRIGGER review_atomic_abort`)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	q = NewQueue(s, seal, &fakeAudit{})
	q.SetRunner(func(c context.Context, j *Job) error {
		op, e := s.LoadOperation(c, j.ID)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(op.Identity, before.Identity) || op.Checkpoint != "verified" {
			return errors.New("identity/checkpoint changed")
		}
		return nil
	})
	j, e = reviewRunTerminal(t, q, s, r.Job.ID)
	if e != nil || j.State != "done" {
		t.Fatalf("reopen completion: %s %v", j.State, e)
	}
	got, e := q.Claim(ctx, j.ID, r.ClaimToken)
	if e != nil || !reflect.DeepEqual(got, map[string]any{"password": "exact-password"}) {
		t.Fatalf("claim=%v error=%v", got, e)
	}
	if handled, e := q.operationTerminal(ctx, j); !handled || e != nil {
		t.Fatal("done replay", handled, e)
	}
	if _, e = q.Claim(ctx, j.ID, r.ClaimToken); e == nil {
		t.Fatal("done replay regenerated consumed claim")
	}
	if commands != 1 {
		t.Fatal("duplicate effect")
	}
	var counts [3]int
	if e = s.Tx(ctx, func(tx *sql.Tx) error {
		for i, table := range []string{"databases", "db_users", "jobs"} {
			if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&counts[i]); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if counts != [3]int{1, 1, 1} {
		t.Fatal("duplicate rows", counts)
	}
	raw, _ := json.Marshal(j)
	if strings.Contains(string(raw), "exact-password") || strings.Contains(string(raw), "secret-salt") {
		t.Fatal("secret in job")
	}
	t.Logf("rollback/reopen/done replay: command effects=%d rows=%v password-only one-shot claim verified", commands, counts)
}

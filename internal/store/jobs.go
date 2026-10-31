package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type Job struct {
	ID         string          `json:"id"`
	Verb       string          `json:"verb"`
	ObjectKey  string          `json:"object_key"`
	Actor      string          `json:"-"`
	Role       string          `json:"-"`
	CreatedBy  string          `json:"-"`
	State      string          `json:"state"`
	Step       string          `json:"step"`
	Error      string          `json:"error,omitempty"`
	Code       string          `json:"code,omitempty"`
	Args       json.RawMessage `json:"-"`
	Result     json.RawMessage `json:"result,omitempty"`
	Effectful  bool            `json:"-"`
	Attempts   int             `json:"attempts"`
	Progress   float64         `json:"progress"`
	Created    int64           `json:"created"`
	Updated    int64           `json:"updated"`
	Finished   *int64          `json:"finished,omitempty"`
	Scopes     []string        `json:"-"`
	Automation bool            `json:"-"`
}
type JobFilter struct {
	State, Verb, Before string
	Limit               int
}

const jobColumns = "id,verb,args_json,object_key,effectful,actor,coalesce(role,''),coalesce(created_by,''),state,step,attempts,progress,result_json,coalesce(error,''),coalesce(code,''),created,updated,finished,scopes_json,automation"

type scanner interface{ Scan(...any) error }

func scanJob(r scanner) (j Job, e error) {
	var a, sc string
	var result sql.NullString
	e = r.Scan(&j.ID, &j.Verb, &a, &j.ObjectKey, &j.Effectful, &j.Actor, &j.Role, &j.CreatedBy, &j.State, &j.Step, &j.Attempts, &j.Progress, &result, &j.Error, &j.Code, &j.Created, &j.Updated, &j.Finished, &sc, &j.Automation)
	if e != nil {
		return
	}
	j.Args = json.RawMessage(a)
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	e = json.Unmarshal([]byte(sc), &j.Scopes)
	return
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Store) CreateJob(ctx context.Context, j Job) error { return insertJob(ctx, s.db, j) }

// CreateJobWithClaim publishes the queued job and hashed token in one transaction.
func (s *Store) CreateJobWithClaim(ctx context.Context, j Job, c JobClaim) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if c.JobID != j.ID {
			return errors.New("claim job mismatch")
		}
		if e := insertJob(ctx, tx, j); e != nil {
			return e
		}
		c.Expires = 0
		c.Nonce = nil
		c.Ciphertext = nil
		return insertClaim(ctx, tx, c)
	})
}
func insertJob(ctx context.Context, db execer, j Job) error {
	if j.Scopes == nil {
		j.Scopes = []string{}
	}
	sc, e := json.Marshal(j.Scopes)
	if e != nil {
		return e
	}
	if j.State == "" {
		j.State = "queued"
	}
	_, e = db.ExecContext(ctx, `INSERT INTO jobs(id,verb,args_json,object_key,effectful,actor,role,created_by,state,step,attempts,progress,result_json,error,code,created,updated,finished,scopes_json,automation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, j.ID, j.Verb, string(j.Args), j.ObjectKey, j.Effectful, j.Actor, j.Role, j.CreatedBy, j.State, j.Step, j.Attempts, j.Progress, nullableJSON(j.Result), j.Error, j.Code, j.Created, j.Updated, j.Finished, string(sc), j.Automation)
	return e
}
func nullableJSON(j json.RawMessage) any {
	if len(j) == 0 {
		return nil
	}
	return string(j)
}
func (s *Store) GetJob(ctx context.Context, id string) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=?", id))
}
func (s *Store) NextQueuedJob(ctx context.Context) (Job, error) {
	return scanJob(s.db.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE state='queued' ORDER BY created,id LIMIT 1"))
}
func (s *Store) StartJob(ctx context.Context, id string, now int64) (j Job, e error) {
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, "UPDATE jobs SET state='running', attempts=attempts+1, updated=? WHERE id=? AND state='queued'", now, id)
		if e = affected(r, e); e != nil {
			return e
		}
		j, e = scanJob(tx.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id=?", id))
		return e
	})
	return
}
func affected(r sql.Result, e error) error {
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	q := "SELECT " + jobColumns + " FROM jobs WHERE 1=1"
	a := []any{}
	for _, p := range []struct{ k, v string }{{"state", f.State}, {"verb", f.Verb}} {
		if p.v != "" {
			q += " AND " + p.k + "=?"
			a = append(a, p.v)
		}
	}
	if f.Before != "" {
		q += " AND id<?"
		a = append(a, f.Before)
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	q += " ORDER BY id DESC LIMIT ?"
	a = append(a, f.Limit)
	rows, e := s.db.QueryContext(ctx, q, a...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// UpdateJob clears sealed inputs in the same write as a terminal transition.
// An interrupted failure is operational retry state and keeps its input.
func (s *Store) UpdateJob(ctx context.Context, j Job) error {
	terminal := j.State == "done" || (j.State == "failed" && j.Code != "interrupted")
	r, e := s.db.ExecContext(ctx, `UPDATE jobs SET args_json=CASE WHEN ? AND json_valid(args_json) THEN CASE WHEN json_extract(args_json,'$._sealed_job_input')=1 THEN '{}' ELSE args_json END ELSE args_json END,state=?,step=?,progress=?,result_json=?,error=?,code=?,updated=?,finished=? WHERE id=?`, terminal, j.State, j.Step, j.Progress, nullableJSON(j.Result), j.Error, j.Code, j.Updated, j.Finished, j.ID)
	return affected(r, e)
}
func (s *Store) RecoverJobs(ctx context.Context, now int64) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		// A validated typed journal owns roll-forward, including secret-input jobs.
		// Locally contained post-effect failures finish as failed/interrupted,
		// unlike process interruption's running row. Both retain the original
		// active journal. Other terminal failures and expired journals never retry.
		if _, e := tx.ExecContext(ctx, `UPDATE jobs SET state='queued',code=NULL,error=NULL,finished=NULL,updated=? WHERE (state='running' OR (state='failed' AND code='interrupted')) AND EXISTS(SELECT 1 FROM phase4_operations o WHERE o.job_id=jobs.id AND o.checkpoint NOT IN ('done','expired'))`, now); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE jobs SET state=CASE WHEN effectful=1 THEN 'failed' ELSE 'queued' END,code=CASE WHEN effectful=1 THEN 'interrupted' ELSE NULL END,error=CASE WHEN effectful=1 THEN 'execution interrupted by restart' ELSE NULL END,finished=CASE WHEN effectful=1 THEN ? ELSE NULL END,updated=? WHERE state='running'`, now, now)
		return e
	})
}

type JobEvent struct {
	ID, JobID, Msg string
	TS             int64
}

func (s *Store) AppendJobEvent(ctx context.Context, v JobEvent) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO job_events(id,job_id,ts,msg) VALUES(?,?,?,?)", v.ID, v.JobID, v.TS, v.Msg)
	return e
}
func (s *Store) JobEvents(ctx context.Context, id string) ([]JobEvent, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT id,job_id,ts,msg FROM job_events WHERE job_id=? ORDER BY ts,id", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []JobEvent{}
	for rows.Next() {
		var v JobEvent
		if e = rows.Scan(&v.ID, &v.JobID, &v.TS, &v.Msg); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type JobClaim struct {
	JobID, ClaimHash  string
	Nonce, Ciphertext []byte
	Expires           int64
}

func (s *Store) PutClaim(ctx context.Context, v JobClaim) error { return insertClaim(ctx, s.db, v) }
func insertClaim(ctx context.Context, db execer, v JobClaim) error {
	if v.Nonce == nil {
		v.Nonce = []byte{}
	}
	if v.Ciphertext == nil {
		v.Ciphertext = []byte{}
	}
	_, e := db.ExecContext(ctx, "INSERT INTO job_claims(job_id,claim_hash,nonce,ciphertext,expires) VALUES(?,?,?,?,?)", v.JobID, v.ClaimHash, v.Nonce, v.Ciphertext, v.Expires)
	return e
}

// ClaimTTLMillis is the short show-once retrieval window, starting at publication.
const ClaimTTLMillis int64 = 5 * 60 * 1000

func (s *Store) PublishClaim(ctx context.Context, id string, nonce, ciphertext []byte) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if len(nonce) == 0 || len(ciphertext) == 0 {
			return errors.New("empty sealed claim")
		}
		// Acquire the write lock before reading the clock; pool/SQLite waits cannot age this timestamp.
		if _, e := tx.ExecContext(ctx, "UPDATE job_claims SET claimed=claimed WHERE job_id=?", id); e != nil {
			return e
		}
		now := s.clock.Now()
		r, e := tx.ExecContext(ctx, "UPDATE job_claims SET nonce=?,ciphertext=?,expires=? WHERE job_id=? AND claimed=0 AND length(ciphertext)=0", nonce, ciphertext, now+ClaimTTLMillis, id)
		return affected(r, e)
	})
}
func (s *Store) ConsumeClaim(ctx context.Context, id, hash, owner string) (v JobClaim, e error) {
	missing := false
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE job_claims SET claimed=claimed WHERE job_id=?", id); e != nil {
			return e
		}
		now := s.clock.Now()
		if _, e := tx.ExecContext(ctx, "DELETE FROM job_claims WHERE job_id=? AND length(ciphertext)>0 AND expires<=?", id, now); e != nil {
			return e
		}
		err := tx.QueryRowContext(ctx, `DELETE FROM job_claims WHERE job_id=? AND claim_hash=? AND expires>? AND claimed=0 AND length(ciphertext)>0 AND EXISTS(SELECT 1 FROM jobs WHERE id=job_id AND created_by=? AND created_by<>'') RETURNING job_id,claim_hash,nonce,ciphertext,expires`, id, hash, now, owner).Scan(&v.JobID, &v.ClaimHash, &v.Nonce, &v.Ciphertext, &v.Expires)
		if errors.Is(err, sql.ErrNoRows) {
			missing = true
			return nil
		}
		return err
	})
	if e == nil && missing {
		e = sql.ErrNoRows
	}
	return
}
func (s *Store) DeleteExpiredClaims(ctx context.Context) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "UPDATE job_claims SET claimed=claimed WHERE 0"); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM job_claims WHERE length(ciphertext)>0 AND expires<=?", s.clock.Now())
		return e
	})
}

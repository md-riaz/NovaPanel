package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"time"
)

// ErrRetry retains a running job for roll-forward when cleanup could not finish.
var ErrRetry = errors.New("jobs: retry required")

// Run owns one worker. SQL transactions never span the external runner.
func (q *Queue) Run(ctx context.Context) error {
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return errors.New("jobs: worker already running")
	}
	if q.runner == nil {
		q.mu.Unlock()
		return errors.New("jobs: runner not configured")
	}
	q.running = true
	q.mu.Unlock()
	defer func() { q.mu.Lock(); q.running = false; q.mu.Unlock() }()
	// Refuse even recovery mutations if the audit sinks have failed.
	if q.audit == nil {
		return errors.New("jobs: audit not configured")
	}
	if e := q.audit.Healthy(); e != nil {
		return e
	}
	for q.startup != nil {
		if ctx.Err() != nil {
			return nil
		}
		j, err := q.startup(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.Join(ErrRecoveryBlocked, err)
		}
		if j == nil {
			break
		}
		if j != nil {
			if err = q.recoverPublication(ctx, *j); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return errors.Join(ErrRecoveryBlocked, err)
			}
		}
	}
	if e := q.store.RecoverJobs(ctx, q.clock.Now()); e != nil {
		if ctx.Err() != nil {
			return nil
		}
		return e
	}
	if q.postRecovery != nil {
		if err := q.postRecovery(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return errors.Join(ErrRecoveryBlocked, err)
		}
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		j, e := q.store.NextQueuedJob(ctx)
		if errors.Is(e, sql.ErrNoRows) {
			select {
			case <-ctx.Done():
				return nil
			case <-q.wake:
			case <-tick.C:
			}
			continue
		}
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		if e = q.execute(ctx, j); e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
	}
}
func (q *Queue) execute(ctx context.Context, j Job) error {
	q.mu.Lock()
	deadline := q.attemptDeadline
	q.mu.Unlock()
	attemptCtx := ctx
	if deadline != nil {
		if limit := deadline(j.Verb); limit > 0 {
			var cancel context.CancelFunc
			attemptCtx, cancel = context.WithTimeout(ctx, limit)
			defer cancel()
		}
	}
	if key := databaseSiteKey(j); key != "" {
		unlockSite, err := q.locks.lock(attemptCtx, key)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		defer unlockSite()
	}
	unlock, err := q.locks.lock(attemptCtx, j.ObjectKey)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	}
	defer unlock()
	if e := q.healthy(); e != nil {
		return e
	}
	started, e := q.store.StartJob(ctx, j.ID, q.clock.Now())
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	j = started
	q.mu.Lock()
	run := q.runner
	q.mu.Unlock()
	// Caller identity is restored exclusively from durable trusted columns.
	err = invoke(WithMetadata(attemptCtx, metadata(j)), run, &j)
	if ctx.Err() != nil {
		return nil
	} // leave running for restart's effectful/idempotent recovery
	if j.Effectful && errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		return q.finish(ctx, j, rpcerr.Errf("interrupted", "Effectful job interrupted"))
	}
	if errors.Is(err, ErrRetry) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
		current, getErr := q.store.GetJob(ctx, j.ID)
		if getErr != nil {
			return getErr
		}
		current.State = "queued"
		current.Updated = q.clock.Now()
		return q.store.UpdateJob(ctx, current)
	}
	return q.finish(ctx, j, err)
}

func databaseSiteKey(j Job) string {
	if j.Verb != "database.create" && j.Verb != "database.delete" && j.Verb != "database.rotate_password" && j.Verb != "dbuser.create" && j.Verb != "dbuser.delete" {
		return ""
	}
	var args struct {
		SiteID string `json:"site_id"`
	}
	if json.Unmarshal(j.Args, &args) != nil || !id.Valid(args.SiteID) {
		return ""
	}
	return "site:" + args.SiteID
}

func (q *Queue) finish(ctx context.Context, j Job, err error) error {
	now := q.clock.Now()
	j.Updated = now
	j.Finished = &now
	outcome := "ok"
	j.State = "done"
	j.Code = ""
	j.Error = ""
	j.Progress = 1
	if err != nil {
		outcome = "error"
		j.State = "failed"
		j.Code = "agent_error"
		j.Error = "job execution failed"
		var coded *rpcerr.CodedError
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			j.Code = "deadline_exceeded"
		} else if errors.As(err, &coded) {
			j.Code = coded.Code
		}
	}
	if err != nil && j.Verb == "site.delete" {
		// Only the explicitly permitted, validated database IDs may survive a refusal.
		var result struct {
			IDs []string `json:"attached_database_ids"`
		}
		safe := j.Code == "precondition_failed" && json.Unmarshal(j.Result, &result) == nil && len(result.IDs) > 0
		for _, value := range result.IDs {
			safe = safe && id.Valid(value)
		}
		j.Result = nil
		if safe {
			j.Result, _ = json.Marshal(result)
		}
	}
	// Do not persist external error text or panic payloads, which can contain secrets.
	if e := q.record(j, outcome, j.Code, "exec"); e != nil {
		return e
	}
	eid, e := id.New()
	if e != nil {
		return e
	}
	if e = q.store.AppendJobEvent(ctx, store.JobEvent{ID: eid, JobID: j.ID, TS: now, Msg: j.State}); e != nil {
		return e
	}
	return q.store.UpdateJob(ctx, j)
}
func invoke(ctx context.Context, run func(context.Context, *Job) error, j *Job) (err error) {
	defer func() {
		if recover() != nil {
			err = rpcerr.Errf("agent_error", "job execution panicked")
		}
	}()
	if run == nil {
		return errors.New("jobs: runner not configured")
	}
	return run(ctx, j)
}

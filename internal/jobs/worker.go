package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"time"
)

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
	if err := q.PrepareStartup(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	expiryTick := time.NewTicker(time.Minute)
	defer expiryTick.Stop()
	for {
		select {
		case <-expiryTick.C:
			if e := q.expireOperations(ctx); e != nil {
				if ctx.Err() != nil {
					return nil
				}
				return e
			}
		default:
		}
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
	unlock, keyErr := q.lockJob(ctx, j)
	if keyErr != nil {
		var coded *rpcerr.CodedError
		if errors.As(keyErr, &coded) && coded.Code == "unsupported" {
			started, err := q.store.StartJob(ctx, j.ID, q.clock.Now())
			if err != nil {
				return err
			}
			return q.finish(ctx, started, keyErr)
		}
		return keyErr
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
	err := error(nil)
	isOperation := false
	if s, ok := q.store.(operationStore); ok {
		if op, loadErr := s.LoadOperation(ctx, j.ID); loadErr == nil {
			isOperation = true
			err = s.CheckOperation(ctx, j.ID)
			if err == nil && len(op.Ciphertext) > 0 {
				if op.Kind == store.WordPressInstall {
					_, err = q.OpenWordPressCredentials(ctx, j.ID)
				} else {
					_, err = q.OpenOperationCredentials(ctx, j.ID)
				}
			}
		} else if !errors.Is(loadErr, sql.ErrNoRows) {
			return loadErr
		}
	}
	if err == nil {
		err = invoke(WithMetadata(ctx, metadata(j)), run, &j)
	}
	if ctx.Err() != nil {
		return nil
	} // leave running for restart's effectful/idempotent recovery
	if isOperation {
		err = operationError(err)
	}
	return q.finish(ctx, j, err)
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
		if errors.As(err, &coded) {
			j.Code = coded.Code
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
	if handled, e := q.operationTerminal(ctx, j); handled {
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

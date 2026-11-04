package jobs

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/render/nginx"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"time"
)

var errPHPRestored = errors.New("jobs: PHP original restored and failure reconciled")

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
			guardCtx, release, e := q.AdmitMutation(ctx)
			if e == nil {
				e = q.expireOperations(guardCtx)
				release()
				if e != nil && ctx.Err() == nil {
					return e
				}
				if ctx.Err() != nil {
					return nil
				}
				break
			}
			if e != nil {
				if ctx.Err() != nil {
					return nil
				}
				if !errors.Is(e, ErrRecoveryBlocked) {
					return e
				}
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
		workCtx, release, e := q.AdmitMutation(ctx)
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !errors.Is(e, ErrRecoveryBlocked) {
				return e
			}
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
			continue
		}
		e = q.execute(workCtx, j)
		release()
		if e != nil && !errors.Is(e, errPHPRestored) {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
	}
}
func (q *Queue) execute(ctx context.Context, j Job) error {
	ctx, release, err := q.AdmitMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	unlock, keyErr := q.lockJob(ctx, j)
	if keyErr != nil {
		var coded *rpcerr.CodedError
		if errors.As(keyErr, &coded) && coded.Code == "unsupported" {
			if err := q.healthy(); err != nil {
				return err
			}
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
	err = nil
	isOperation := false
	phpRecovery := false
	if s, ok := q.store.(operationStore); ok {
		if op, loadErr := s.LoadOperation(ctx, j.ID); loadErr == nil {
			isOperation = true
			if op.Kind == store.PHPSet {
				phpRecovery = op.Checkpoint == "config_published" || op.Checkpoint == "verified"
				request, decodeErr := php.DecodeSetRequest(j.Args)
				if decodeErr != nil || op.Identity.PHP == nil || request.SiteID != op.SiteID || j.ObjectKey != "site:"+op.SiteID || request.Version != op.Identity.PHP.DesiredVersion {
					err = rpcerr.Errf("invalid_argument", "invalid stored PHP request")
				} else {
					merged, mergeErr := php.MergeLimits(&op.Identity.PHP.OriginalLimits, request.Limits)
					if mergeErr != nil || merged != op.Identity.PHP.DesiredLimits {
						err = rpcerr.Errf("invalid_argument", "PHP request does not match journal")
					}
				}
			}
			if err == nil {
				err = s.CheckOperation(ctx, j.ID)
			}
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
		workerCtx, cancel := context.WithCancel(WithMetadata(ctx, metadata(j)))
		lease := &configWorkerLease{queue: q, job: j.ID, cancel: cancel}
		lease.ctx = context.WithValue(workerCtx, configWorkerKey{}, lease)
		lease.active.Store(true)
		err = func() error {
			defer lease.close()
			return invoke(lease.ctx, run, &j)
		}()
	}
	if ctx.Err() != nil {
		return nil
	} // leave running for restart's effectful/idempotent recovery
	if errors.Is(err, nginx.ErrPHPRecoveryRequired) {
		return q.latchPublication(errors.Join(ErrRecoveryBlocked, err))
	}
	if phpRecovery && !errors.Is(err, nginx.ErrPHPOriginalRestored) {
		return q.latchPublication(errors.Join(ErrRecoveryBlocked, err))
	}
	if isOperation {
		err = operationError(err)
	}
	if errors.Is(err, nginx.ErrPHPOriginalRestored) {
		if e := q.finish(ctx, j, err); e != nil {
			return e
		}
		return errPHPRestored
	}
	return q.finish(ctx, j, err)
}

func (q *Queue) finish(ctx context.Context, j Job, err error) error {
	// The executor may have changed eligibility after its last checkpoint.
	// Revalidate under the still-held operation locks before choosing the audit
	// outcome. CompleteOperation retains its transaction-local checks.
	if err == nil {
		if s, ok := q.store.(operationStore); ok {
			if op, loadErr := s.LoadOperation(ctx, j.ID); loadErr == nil {
				if checkErr := s.CheckOperation(ctx, j.ID); checkErr != nil {
					// A PHP executor may already have published live bytes. Its
					// durable original must remain available for restoration; a
					// generic failure would otherwise discard recovery authority.
					if op.Kind == store.PHPSet && (op.Checkpoint == "config_published" || op.Checkpoint == "verified") {
						return q.latchPublication(errors.Join(ErrRecoveryBlocked, checkErr))
					}
					mapped := operationError(checkErr)
					var coded *rpcerr.CodedError
					if !errors.As(mapped, &coded) {
						return checkErr
					}
					err = mapped
				}
			} else if !errors.Is(loadErr, sql.ErrNoRows) {
				return loadErr
			}
		}
	}
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
	if err == nil {
		if handled, e := q.finishPublication(ctx, j); handled {
			return e
		}
	}
	if errors.Is(err, nginx.ErrPHPOriginalRestored) {
		return q.finishRestoredPHP(ctx, j)
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
		if e != nil {
			return e
		}
		return q.reconcileConfigRecord(ctx, j.ID)
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

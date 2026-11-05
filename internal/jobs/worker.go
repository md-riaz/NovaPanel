package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"sort"
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
		highWater, err := q.store.QueuedJobsHighWater(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		var cursor int64
		didWork := false
		for {
			page, err := q.store.ListQueuedJobs(ctx, cursor, highWater)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			if len(page) == 0 {
				break
			}
			for _, entry := range page {
				cursor = entry.RowID
				if ctx.Err() != nil {
					return nil
				}
				worked, err := q.executeEligible(ctx, entry.Job)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				didWork = didWork || worked
			}
			if len(page) < store.QueuedJobsPageSize {
				break
			}
		}
		// A pass that ran only retrying jobs must not spin, but new admissions
		// still wake the worker promptly.
		if !didWork {
			select {
			case <-ctx.Done():
				return nil
			case <-q.wake:
			case <-tick.C:
			}
		} else {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
			}
		}
	}
}

type heldLocksKey struct{}

func (q *Queue) lockSecondary(ctx context.Context, key string) (func(), error) {
	if held, ok := ctx.Value(heldLocksKey{}).(map[string]bool); ok && held[key] {
		return func() {}, nil
	}
	return q.locks.lock(ctx, key)
}

// executeEligible holds every execution lock through the invocation. In particular,
// cascade users are read only after holding the parent, so the list cannot stale.
func (q *Queue) executeEligible(ctx context.Context, j Job) (bool, error) {
	held := map[string]bool{}
	var releases []func()
	defer func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	acquire := func(key string) bool {
		if key == "" || held[key] {
			return true
		}
		release, ok := q.locks.tryLock(key)
		if ok {
			held[key] = true
			releases = append(releases, release)
		}
		return ok
	}
	if !acquire(databaseSiteKey(j)) || !acquire(j.ObjectKey) {
		return false, nil
	}
	var args struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		DBID     string `json:"db_id"`
	}
	if json.Unmarshal(j.Args, &args) == nil {
		switch j.Verb {
		case "database.create":
			if args.Name != "" && !acquire("dbname:"+args.Name) {
				return false, nil
			}
			if args.Username != "" && !acquire("dbuser:"+args.Username) {
				return false, nil
			}
		case "database.delete":
			p, ok := q.store.(interface {
				DatabaseWithUsers(context.Context, string) (store.Database, []store.DatabaseUser, error)
			})
			if !ok {
				return false, errors.New("jobs: database enumeration unavailable")
			}
			d, users, err := p.DatabaseWithUsers(ctx, args.DBID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return false, err
			}
			if err == nil {
				if !acquire("dbname:" + d.Name) {
					return false, nil
				}
				sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
				for _, u := range users {
					if !acquire("dbuser:" + u.Username) {
						return false, nil
					}
				}
			}
		case "database.rotate_password", "dbuser.create", "dbuser.delete":
			if args.Username != "" && !acquire("dbuser:"+args.Username) {
				return false, nil
			}
		}
	}
	return true, q.execute(context.WithValue(ctx, heldLocksKey{}, held), j)
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
		unlockSite, err := q.lockSecondary(attemptCtx, key)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		defer unlockSite()
	}
	unlock, err := q.lockSecondary(attemptCtx, j.ObjectKey)
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
	var coded *rpcerr.CodedError
	if errors.As(err, &coded) && coded.Code == "interrupted" {
		// A journaled operation requires recovery, even if its attempt also expired.
		return q.finish(ctx, j, rpcerr.Errf("interrupted", "Job requires recovery"))
	}
	transient := errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || (coded != nil && coded.Code == "deadline_exceeded")
	if j.Effectful && transient {
		return q.finish(ctx, j, rpcerr.Errf("interrupted", "Effectful job interrupted"))
	}
	if errors.Is(err, ErrRetry) || transient {
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

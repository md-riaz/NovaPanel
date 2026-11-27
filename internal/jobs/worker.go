package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/render/nginx"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"reflect"
	"sort"
	"time"
)

var errPHPRestored = errors.New("jobs: PHP original restored and failure reconciled")

// ErrRetry retains a running job for roll-forward when cleanup could not finish.
var ErrRetry = rpcerr.ErrRetry

// ErrRecoveryRequired marks a journaled publication requiring dedicated recovery.
var ErrRecoveryRequired = errors.New("jobs: recovery required")

type freshResumeCheckpointKey struct{}

// FreshResumeCheckpoint carries the original checkpoint only within this locked attempt.
func FreshResumeCheckpoint(ctx context.Context) string {
	value, _ := ctx.Value(freshResumeCheckpointKey{}).(string)
	return value
}

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
				workCtx, release, admissionErr := q.AdmitMutation(ctx)
				if admissionErr != nil {
					if ctx.Err() != nil {
						return nil
					}
					if errors.Is(admissionErr, ErrRecoveryBlocked) {
						continue
					}
					return admissionErr
				}
				worked, err := q.executeEligible(workCtx, entry.Job)
				release()
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
type executionParentKey struct{}
type operationUnappliedKey struct{}

func (q *Queue) lockSecondary(ctx context.Context, key string) (func(), error) {
	if held, ok := ctx.Value(heldLocksKey{}).(map[string]bool); ok && held[key] {
		return func() {}, nil
	}
	return q.locks.lockContext(ctx, key)
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
	keys, err := q.operationKeys(ctx, j)
	if err != nil {
		return false, err
	}
	for _, key := range keys {
		if !acquire(key) {
			return false, nil
		}
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
	parent := ctx
	if original, ok := ctx.Value(executionParentKey{}).(context.Context); ok {
		parent = original
	}
	return true, q.executeOperation(context.WithValue(parent, heldLocksKey{}, held), j, false)
}

func (q *Queue) execute(ctx context.Context, j Job) error {
	ctx, release, err := q.AdmitMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	waitCtx := ctx
	q.mu.Lock()
	deadline := q.attemptDeadline
	q.mu.Unlock()
	if deadline != nil {
		if limit := deadline(j.Verb); limit > 0 {
			var cancel context.CancelFunc
			waitCtx, cancel = context.WithTimeout(ctx, limit)
			defer cancel()
		}
	}
	waitCtx = context.WithValue(waitCtx, executionParentKey{}, ctx)
	for {
		worked, err := q.executeEligible(waitCtx, j)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return nil
			}
			return err
		}
		if worked {
			return nil
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (q *Queue) executeOperation(ctx context.Context, j Job, restoreExpiredPHP bool) error {
	ctx, release, err := q.AdmitMutation(ctx)
	if err != nil {
		return err
	}
	defer release()
	attemptCtx := ctx
	q.mu.Lock()
	deadline := q.attemptDeadline
	q.mu.Unlock()
	if deadline != nil {
		if limit := deadline(j.Verb); limit > 0 {
			var cancel context.CancelFunc
			attemptCtx, cancel = context.WithTimeout(ctx, limit)
			defer cancel()
		}
	}
	unlock, keyErr := q.lockJob(attemptCtx, j)
	if keyErr != nil {
		if errors.Is(keyErr, context.DeadlineExceeded) && ctx.Err() == nil {
			return nil
		}
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
	if _, ok := attemptCtx.Value(heldLocksKey{}).(map[string]bool); !ok {
		keys, keyErr := q.operationKeys(attemptCtx, j)
		if keyErr != nil {
			return keyErr
		}
		held := make(map[string]bool, len(keys))
		for _, key := range keys {
			held[key] = true
		}
		attemptCtx = context.WithValue(attemptCtx, heldLocksKey{}, held)
	}
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
	wordpressInstall := false
	compositeUpdate := false
	freshInstall := false
	phpRecovery := false
	if s, ok := q.store.(operationStore); ok {
		if op, loadErr := s.LoadOperation(ctx, j.ID); loadErr == nil {
			isOperation = true
			compositeUpdate = op.Kind == store.SiteUpdate
			wordpressInstall = op.Kind == store.WordPressInstall && j.Verb == "site.create"
			if op.Kind == store.WordPressInstall && j.Verb == "site.create" {
				if fresh, ok := q.store.(interface {
					LoadSiteLifetime(context.Context, string) (store.SiteLifetime, error)
					CheckFreshSiteLifetime(context.Context, store.SiteLifetime) error
					BeginFreshSitePreflight(context.Context, string, store.SiteLifetime) (string, error)
				}); ok {
					identity, e := fresh.LoadSiteLifetime(ctx, op.SiteID)
					if e == nil && identity.AllocatingJob == j.ID {
						previous := ""
						if j.Step != store.FreshWordPressRollbackIntentStep && j.Step != store.FreshWordPressRollbackStep && j.Step != store.FreshWordPressRollbackCleanedStep {
							var preflightErr error
							previous, preflightErr = fresh.BeginFreshSitePreflight(ctx, j.ID, identity)
							if preflightErr != nil {
								return errors.Join(ErrRetry, preflightErr)
							}
							if previous != "" {
								attemptCtx = context.WithValue(attemptCtx, freshResumeCheckpointKey{}, previous)
							}
						}
						freshInstall = previous != "" || fresh.CheckFreshSiteLifetime(ctx, identity) == nil
					}
				}
			}
			if freshInstall && j.Step == store.FreshWordPressRollbackIntentStep {
				if e := q.beginFreshRollback(ctx, j, j.Code); e != nil {
					return e
				}
				return nil
			}
			rollback := freshInstall && (j.Step == store.FreshWordPressRollbackStep || j.Step == store.FreshWordPressRollbackCleanedStep)
			if restoreExpiredPHP && (op.Kind != store.PHPSet || j.Verb != "php.set") {
				return ErrRecoveryBlocked
			}
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
			if op.Kind == store.SiteUpdate {
				var request store.CompositeSiteUpdate
				if json.Unmarshal(j.Args, &request) != nil || j.ObjectKey != "site:"+op.SiteID || !reflect.DeepEqual(&request, op.Identity.SiteUpdate) {
					err = rpcerr.Errf("invalid_argument", "site update does not match journal")
				}
			}
			if err == nil && !rollback {
				if restoreExpiredPHP {
					check, ok := q.store.(interface {
						CheckExpiredPHPRestoration(context.Context, string) error
					})
					if !ok {
						return ErrRecoveryBlocked
					}
					err = check.CheckExpiredPHPRestoration(ctx, j.ID)
				} else {
					err = s.CheckOperation(ctx, j.ID)
				}
			}
			if err == nil && !rollback && len(op.Ciphertext) > 0 {
				if op.Kind == store.WordPressInstall {
					_, err = q.OpenWordPressCredentials(ctx, j.ID)
				} else {
					_, err = q.OpenOperationCredentials(ctx, j.ID)
				}
			}
		} else if restoreExpiredPHP || !errors.Is(loadErr, sql.ErrNoRows) {
			return loadErr
		}
	} else if restoreExpiredPHP {
		return ErrRecoveryBlocked
	}
	if err != nil && isOperation && j.Attempts == 1 && !restoreExpiredPHP {
		if journal, ok := q.store.(operationStore); ok {
			if op, e := journal.LoadOperation(ctx, j.ID); e == nil && op.Checkpoint == "prepared" {
				ctx = context.WithValue(ctx, operationUnappliedKey{}, true)
			}
		}
	}
	if err == nil {
		workerCtx, cancel := context.WithCancel(WithMetadata(attemptCtx, metadata(j)))
		lease := &configWorkerLease{queue: q, job: j.ID, cancel: cancel, restoreExpiredPHP: restoreExpiredPHP}
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
		retryFresh := wordpressInstall && errors.Is(err, ErrRetry)
		err = operationError(err)
		if retryFresh && !errors.Is(err, ErrRetry) {
			err = errors.Join(err, ErrRetry)
		}
		if err != nil {
			if journal, ok := q.store.(operationStore); ok {
				if op, e := journal.LoadOperation(ctx, j.ID); e == nil && store.TeardownStarted(op) {
					err = errors.Join(err, ErrRetry)
				}
			}
		}
	}
	if freshInstall && err != nil && !errors.Is(err, ErrRetry) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		switch j.Step {
		case store.FreshWordPressRollbackStep:
			err = errors.Join(err, ErrRetry)
		case store.FreshWordPressRollbackCleanedStep:
			// The rollback executor has finished external teardown and custody cleanup.
		default:
			code := "agent_error"
			var coded *rpcerr.CodedError
			if errors.As(err, &coded) {
				code = coded.Code
			}
			if e := q.beginFreshRollback(ctx, j, code); e != nil {
				return e
			}
			return nil
		}
	}
	if wordpressInstall && errors.Is(err, ErrRetry) {
		return q.requeueOperation(ctx, j.ID)
	}
	if compositeUpdate && errors.Is(err, ErrRetry) {
		return q.requeueOperation(ctx, j.ID)
	}
	if freshInstall && j.Step == store.FreshWordPressRollbackCleanedStep && err != nil && !errors.Is(err, ErrRetry) {
		return q.finish(ctx, j, err)
	}
	if errors.Is(err, nginx.ErrPHPOriginalRestored) {
		if e := q.finish(ctx, j, err); e != nil {
			return e
		}
		return errPHPRestored
	}
	// F6 precedence across the entire error tree: explicit interruption, then
	// effectful deadline safety / retry, ordinary terminal, and coded deadline.
	if interrupted := codedError(err, func(code string) bool { return code == "interrupted" }); interrupted != nil {
		unapplied, _ := ctx.Value(operationUnappliedKey{}).(bool)
		if j.Effectful || errors.Is(err, ErrRecoveryRequired) || unapplied {
			return q.finish(ctx, j, interrupted)
		}
		err = errors.Join(err, ErrRetry)
	}
	terminal := codedError(err, func(code string) bool { return code != "deadline_exceeded" })
	transient := errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || codedError(err, func(code string) bool { return code == "deadline_exceeded" }) != nil
	if !j.Effectful && terminal != nil && !errors.Is(err, ErrRetry) {
		return q.finish(ctx, j, terminal)
	}
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
	if terminal != nil {
		return q.finish(ctx, j, terminal)
	}
	return q.finish(ctx, j, err)
}

// codedError searches wrapped and joined errors, rather than only the first code.
func codedError(err error, matches func(string) bool) *rpcerr.CodedError {
	if err == nil {
		return nil
	}
	if coded, ok := err.(*rpcerr.CodedError); ok && matches(coded.Code) {
		return coded
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if coded := codedError(child, matches); coded != nil {
				return coded
			}
		}
	} else if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return codedError(wrapped.Unwrap(), matches)
	}
	return nil
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
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			j.Code = "deadline_exceeded"
		} else if errors.As(err, &coded) {
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

func (q *Queue) beginFreshRollback(ctx context.Context, j Job, code string) error {
	if code == "" {
		code = "agent_error"
	}
	s, ok := q.store.(interface {
		PersistFreshWordPressRollbackIntent(context.Context, string, string) error
		BeginFreshWordPressRollback(context.Context, string, string) error
	})
	if !ok {
		return ErrRetry
	}
	if err := s.PersistFreshWordPressRollbackIntent(ctx, j.ID, code); err != nil {
		return errors.Join(ErrRetry, err)
	}
	// The durable fence prevents the install from running again on retry or restart.
	_ = s.BeginFreshWordPressRollback(ctx, j.ID, code)
	return q.requeueOperation(ctx, j.ID)
}

func (q *Queue) requeueOperation(ctx context.Context, jid string) error {
	current, err := q.store.GetJob(ctx, jid)
	if err != nil {
		return err
	}
	current.State = "queued"
	current.Updated = q.clock.Now()
	return q.store.UpdateJob(ctx, current)
}

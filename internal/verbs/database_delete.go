package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/database"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
)

type databaseDeleteJob struct {
	DBID   string  `json:"db_id"`
	Name   string  `json:"name"`
	SiteID *string `json:"site_id,omitempty"`
	Users  []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	} `json:"users,omitempty"`
}

func (v *databaseDeleteJob) Validate() error { return (&databaseIDArgs{DBID: v.DBID}).Validate() }
func init() {
	rpc.Register(rpc.Verb{Name: "database.delete", MinRole: rpc.Admin, Scope: "databases:write", Slow: true, MFA: true, NewArgs: func() any { return &databaseIDArgs{} }, NewJobArgs: func() any { return &databaseDeleteJob{} }, JobObjectKey: func(v any) string { return "db:" + v.(*databaseDeleteJob).DBID }, Run: databaseDelete, Exec: databaseDeleteExec})
}
func databaseDelete(ctx context.Context, a *agent.Agent, args any) (any, error) {
	d, _, e := a.Store.DatabaseWithUsers(ctx, args.(*databaseIDArgs).DBID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, rpc.Errf("not_found", "Database not found")
	}
	if e != nil {
		return nil, e
	}
	return a.Jobs.Enqueue(ctx, "database.delete", databaseDeleteJob{DBID: d.ID, Name: d.Name, SiteID: d.SiteID}, "db:"+d.ID)
}
func databaseDeleteExec(ctx context.Context, a *agent.Agent, j *jobs.Job) (err error) {
	durable, err := a.Store.GetJob(ctx, j.ID)
	if err != nil {
		return jobs.ErrRetry
	}
	// Once teardown may have started, no error may terminalize the job while
	// its SQLite owner survives. F20.9 leaves live jobs to roll forward.
	started := durable.Step == "delete_started"
	defer func() {
		var coded *rpc.CodedError
		if started && err != nil && !(errors.As(err, &coded) && (coded.Code == "conflict" || coded.Code == "precondition_failed")) {
			err = errors.Join(err, jobs.ErrRetry)
		}
	}()
	var v databaseDeleteJob
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return rpc.Errf("bad_request", "Invalid database input")
	}
	if e := v.Validate(); e != nil {
		return e
	}
	if j.ObjectKey != "db:"+v.DBID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	d, users, e := a.Store.DatabaseWithUsers(ctx, v.DBID)
	if errors.Is(e, sql.ErrNoRows) {
		j.Result = json.RawMessage(`{}`)
		return nil
	}
	if e != nil {
		return e
	}
	if d.Name != v.Name {
		return rpc.Errf("precondition_failed", "Database state changed")
	}
	unlockName, e := a.Jobs.LockDatabaseName(ctx, d.Name)
	if e != nil {
		return e
	}
	defer unlockName()
	if e = databaseReady(ctx, a.Exec); e != nil {
		return e
	}
	// A foreign or uncertain parent cannot authorize tearing down its children,
	// nor can its SQLite row be removed while live children would cascade.
	if e = database.CheckSchemaDelete(ctx, a.Exec, d.Name, d.ID); e != nil {
		if errors.Is(e, database.ErrOwnerMismatch) && len(users) == 0 {
			if err := a.Store.DeleteDatabaseForJob(ctx, d.ID, d.Name, j.ID); err != nil {
				return err
			}
		}
		if errors.Is(e, database.ErrConflict) {
			return rpc.Errf("conflict", "Database provenance conflict")
		}
		return e
	}
	if !started {
		durable.Step = "delete_started"
		if e = a.Store.UpdateJob(ctx, durable); e != nil {
			return jobs.ErrRetry
		}
		started = true
	}
	j.Step = durable.Step
	for _, u := range users {
		unlock, err := a.Jobs.LockDatabaseUser(ctx, u.Username)
		if err != nil {
			return err
		}
		e = database.ManagedDeleteUser(ctx, a.Exec, u.Username, u.ID)
		if errors.Is(e, database.ErrOwnerMismatch) {
			err := a.Store.DeleteDatabaseUser(ctx, u)
			unlock()
			if err != nil {
				return err
			}
			return rpc.Errf("conflict", "Database user provenance owner mismatch")
		}
		unlock()
		if errors.Is(e, database.ErrConflict) {
			return rpc.Errf("conflict", "Database user provenance conflict")
		}
		if e != nil {
			return e
		}
	}
	e = database.ManagedDelete(ctx, a.Exec, d.Name, d.ID, nil)
	mismatch := errors.Is(e, database.ErrOwnerMismatch)
	if e != nil && !mismatch {
		if errors.Is(e, database.ErrConflict) {
			return rpc.Errf("conflict", "Database provenance conflict")
		}
		return e
	}
	deleteForJob := a.Store.DeleteDatabaseForJob
	if a.DeleteDatabaseForJob != nil {
		deleteForJob = a.DeleteDatabaseForJob
	}
	if e = deleteForJob(ctx, d.ID, d.Name, j.ID); e != nil {
		return e
	}
	if mismatch {
		return rpc.Errf("conflict", "Database provenance owner mismatch")
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

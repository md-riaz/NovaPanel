package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/database"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
)

type dbuserIDArgs struct {
	UserID string `json:"dbuser_id"`
}

func (v *dbuserIDArgs) Validate() error {
	if !id.Valid(v.UserID) {
		return rpc.Errf("invalid_argument", "Invalid database user ID")
	}
	return nil
}

type dbuserDeleteJob struct {
	UserID   string  `json:"dbuser_id"`
	DBID     string  `json:"db_id"`
	SiteID   *string `json:"site_id,omitempty"`
	DBName   string  `json:"db_name"`
	Username string  `json:"username"`
}

func (v *dbuserDeleteJob) Validate() error {
	if !id.Valid(v.UserID) || !id.Valid(v.DBID) || v.DBName == "" || v.Username == "" {
		return rpc.Errf("invalid_argument", "Invalid database user job")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "dbuser.delete", MinRole: rpc.Admin, Scope: "databases:write", Slow: true, NewArgs: func() any { return &dbuserIDArgs{} }, NewJobArgs: func() any { return &dbuserDeleteJob{} }, JobObjectKey: func(v any) string { return "db:" + v.(*dbuserDeleteJob).DBID }, Run: dbuserDelete, Exec: dbuserDeleteExec})
}
func dbuserDelete(ctx context.Context, a *agent.Agent, args any) (any, error) {
	u, e := a.Store.DatabaseUserByID(ctx, args.(*dbuserIDArgs).UserID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, rpc.Errf("not_found", "Database user not found")
	}
	if e != nil {
		return nil, e
	}
	if u.Owner {
		return nil, rpc.Errf("precondition_failed", "Cannot delete database owner")
	}
	d, _, e := a.Store.DatabaseWithUsers(ctx, u.DatabaseID)
	if e != nil {
		return nil, e
	}
	return a.Jobs.Enqueue(ctx, "dbuser.delete", dbuserDeleteJob{UserID: u.ID, DBID: d.ID, SiteID: d.SiteID, DBName: d.Name, Username: u.Username}, "db:"+d.ID)
}
func dbuserDeleteExec(ctx context.Context, a *agent.Agent, j *jobs.Job) (err error) {
	durable, err := a.Store.GetJob(ctx, j.ID)
	if err != nil {
		return jobs.ErrRetry
	}
	// Retain the live job until both the external teardown and SQLite delete
	// have completed, including across an indeterminate SQL result.
	started := durable.Step == "delete_started"
	defer func() {
		var coded *rpc.CodedError
		if started && err != nil && !(errors.As(err, &coded) && (coded.Code == "conflict" || coded.Code == "precondition_failed")) {
			err = errors.Join(err, jobs.ErrRetry)
		}
	}()
	var v dbuserDeleteJob
	if e := json.Unmarshal(j.Args, &v); e != nil {
		return rpc.Errf("bad_request", "Invalid job input")
	}
	if e := v.Validate(); e != nil {
		return e
	}
	if j.ObjectKey != "db:"+v.DBID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	u, e := a.Store.DatabaseUserByID(ctx, v.UserID)
	if errors.Is(e, sql.ErrNoRows) {
		j.Result = json.RawMessage(`{}`)
		return nil
	}
	if e != nil {
		return e
	}
	if u.Owner {
		return rpc.Errf("precondition_failed", "Cannot delete database owner")
	}
	d, _, e := a.Store.DatabaseWithUsers(ctx, u.DatabaseID)
	if e != nil {
		return e
	}
	if d.ID != v.DBID || d.Name != v.DBName || u.Username != v.Username {
		return rpc.Errf("precondition_failed", "Database user state changed")
	}
	unlock, e := a.Jobs.LockDatabaseUser(ctx, u.Username)
	if e != nil {
		return e
	}
	defer unlock()
	if e = databaseReady(ctx, a.Exec); e != nil {
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
	e = database.ManagedDeleteUser(ctx, a.Exec, u.Username, u.ID)
	mismatch := errors.Is(e, database.ErrOwnerMismatch)
	if e != nil && !mismatch {
		if errors.Is(e, database.ErrConflict) {
			return rpc.Errf("conflict", "Database provenance conflict")
		}
		return e
	}
	if e = a.Store.DeleteDatabaseUser(ctx, u); e != nil {
		return e
	}
	if mismatch {
		return rpc.Errf("conflict", "Database provenance owner mismatch")
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

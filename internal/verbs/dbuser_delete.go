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
	"novapanel/internal/store"
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
	site := ""
	if d.SiteID != nil {
		site = *d.SiteID
	}
	return a.Jobs.EnqueueOperation(ctx, store.OperationRequest{Kind: store.DBUserDelete, DatabaseID: d.ID, UserID: u.ID, DatabaseName: d.Name, Username: u.Username, SiteID: site}, nil)
}
func dbuserDeleteExec(ctx context.Context, a *agent.Agent, j *jobs.Job) (err error) {
	if op, e := a.Store.LoadOperation(ctx, j.ID); e == nil {
		return dbuserDeleteDurable(ctx, a, j, op)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
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

func dbuserDeleteDurable(ctx context.Context, a *agent.Agent, j *jobs.Job, op store.Operation) error {
	if op.Kind != store.DBUserDelete || op.DatabaseID == "" || j.ObjectKey != "db:"+op.DatabaseID {
		return rpc.Errf("precondition_failed", "Invalid deletion journal")
	}
	if op.Checkpoint == "db_applied" {
		return nil
	}
	if err := databaseReady(ctx, a.Exec); err != nil {
		return err
	}
	var target *store.OperationAccount
	for i := range op.Identity.Accounts {
		if op.Identity.Accounts[i].ID == op.Identity.TargetUserID {
			target = &op.Identity.Accounts[i]
			break
		}
	}
	if target == nil || target.ID == op.Identity.Database.PrimaryUserID {
		return rpc.Errf("precondition_failed", "Invalid deletion target")
	}
	if op.Checkpoint == "prepared" {
		if err := a.Store.CheckpointOperation(ctx, j.ID, "prepared", "teardown_started"); err != nil {
			return err
		}
		op.Checkpoint = "teardown_started"
	}
	if op.Checkpoint != "teardown_started" {
		return rpc.Errf("precondition_failed", "Invalid deletion checkpoint")
	}
	if err := database.ManagedDeleteUser(ctx, a.Exec, target.Username, target.ID); err != nil {
		return errors.Join(err, jobs.ErrRetry)
	}
	if err := a.Store.CheckpointOperation(ctx, j.ID, "teardown_started", "db_applied"); err != nil {
		return errors.Join(err, jobs.ErrRetry)
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

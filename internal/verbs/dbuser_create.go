package verbs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/database"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"strings"
)

type dbuserCreateArgs struct {
	DBID     string `json:"db_id"`
	Username string `json:"username,omitempty"`
}

func (v *dbuserCreateArgs) Validate() error {
	if e := (&databaseIDArgs{DBID: v.DBID}).Validate(); e != nil {
		return e
	}
	if v.Username != "" {
		if len(v.Username) > 32 {
			return rpc.Errf("invalid_argument", "Invalid username")
		}
		return (&validate.V{}).DBName(v.Username)
	}
	return nil
}

type dbuserCreateJob struct {
	DBID     string  `json:"db_id"`
	SiteID   *string `json:"site_id,omitempty"`
	DBName   string  `json:"db_name"`
	UserID   string  `json:"user_id"`
	Username string  `json:"username"`
	Password string  `json:"password,omitempty"`
}

func (v *dbuserCreateJob) Validate() error {
	if !id.Valid(v.DBID) || !id.Valid(v.UserID) || v.DBName == "" || v.Username == "" || v.Password == "" {
		return rpc.Errf("invalid_argument", "Invalid database user job")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "dbuser.create", MinRole: rpc.Admin, Scope: "databases:write", Slow: true, SealedInput: true, NewArgs: func() any { return &dbuserCreateArgs{} }, NewJobArgs: func() any { return &dbuserCreateJob{} }, JobObjectKey: func(v any) string { return "db:" + v.(*dbuserCreateJob).DBID }, Run: dbuserCreate, Exec: dbuserCreateExec})
}
func dbuserCreate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*dbuserCreateArgs)
	d, _, e := a.Store.DatabaseWithUsers(ctx, v.DBID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, rpc.Errf("not_found", "Database not found")
	}
	if e != nil {
		return nil, e
	}
	uid, e := id.New()
	if e != nil {
		return nil, e
	}
	name := v.Username
	if name == "" {
		name = "np_" + strings.ToLower(uid[6:22])
	}
	var key [24]byte
	if _, e = rand.Read(key[:]); e != nil {
		return nil, e
	}
	pw := hex.EncodeToString(key[:])
	clear(key[:])
	u := store.DatabaseUser{ID: uid, DatabaseID: d.ID, Username: name}
	result, err := a.Jobs.EnqueueDatabase(ctx, "dbuser.create", dbuserCreateJob{DBID: d.ID, SiteID: d.SiteID, DBName: d.Name, UserID: uid, Username: name}, "db:"+d.ID, pw, nil, &u)
	if errors.Is(err, store.ErrSiteConflict) {
		return nil, rpc.Errf("conflict", "Database user conflict")
	}
	return result, err
}
func dbuserCreateExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	durable, e := a.Store.GetJob(ctx, j.ID)
	if e != nil {
		return jobs.ErrRetry
	}
	if durable.Verb != j.Verb || durable.ObjectKey != j.ObjectKey {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	if durable.Step == "database_committed" {
		j.Step = durable.Step
		j.Result = durable.Result
		return nil
	}
	raw, e := a.Jobs.OpenInput(j)
	if e != nil {
		if durable.Step == "create_started" || errors.Is(e, jobs.ErrRetry) {
			return jobs.ErrRetry
		}
		return rpc.Errf("bad_request", "Invalid sealed input")
	}
	defer clear(raw)
	var v dbuserCreateJob
	if e = json.Unmarshal(raw, &v); e != nil {
		return rpc.Errf("bad_request", "Invalid job input")
	}
	if e = v.Validate(); e != nil {
		return e
	}
	if j.ObjectKey != "db:"+v.DBID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	if e = databaseReady(ctx, a.Exec); e != nil {
		var coded *rpc.CodedError
		if durable.Step == "create_started" && (!errors.As(e, &coded) || coded.Code == "precondition_failed") {
			return jobs.ErrRetry
		}
		return e
	}
	d, _, e := a.Store.DatabaseWithUsers(ctx, v.DBID)
	if errors.Is(e, sql.ErrNoRows) {
		unlock, err := a.Jobs.LockDatabaseUser(ctx, v.Username)
		if err != nil {
			return err
		}
		e = database.ClearUserReservation(ctx, a.Exec, v.Username, v.UserID)
		unlock()
		if e != nil {
			if errors.Is(e, database.ErrConflict) {
				return rpc.Errf("conflict", "Database user state changed")
			}
			if durable.Step == "create_started" {
				return jobs.ErrRetry
			}
			return e
		}
		return rpc.Errf("precondition_failed", "Database no longer exists")
	}
	if e != nil {
		if durable.Step == "create_started" {
			return jobs.ErrRetry
		}
		return e
	}
	if d.Name != v.DBName {
		return rpc.Errf("precondition_failed", "Database user state changed")
	}
	unlock, e := a.Jobs.LockDatabaseUser(ctx, v.Username)
	if e != nil {
		return e
	}
	defer unlock()
	if durable.Step != "create_started" {
		durable.Step = "create_started"
		if e = a.Store.UpdateJob(ctx, durable); e != nil {
			return jobs.ErrRetry
		}
	}
	j.Step = durable.Step
	if e = database.ManagedCreateUser(ctx, a.Exec, v.UserID, v.DBName, v.Username, v.Password); e != nil {
		if errors.Is(e, database.ErrIndeterminate) {
			return jobs.ErrRetry
		}
		code := "agent_error"
		if errors.Is(e, database.ErrConflict) {
			code = "conflict"
		}
		if err := database.AbortCreate(ctx, a.Exec, []database.AbortObject{{Kind: "user", Name: v.Username, ID: v.UserID}}); err != nil {
			return jobs.ErrRetry
		}
		return rpc.Errf(code, "Database user create failed; reconciliation required")
	}
	result, e := json.Marshal(map[string]string{"dbuser_id": v.UserID})
	if e != nil {
		return e
	}
	if e = a.Jobs.PublishDatabaseClaim(ctx, j, result, map[string]any{"password": v.Password}); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The INSERT sentinel is returned before Commit; other errors may be ambiguous.
		if errors.Is(e, store.ErrSiteConflict) {
			if err := database.AbortCreate(ctx, a.Exec, []database.AbortObject{{Kind: "user", Name: v.Username, ID: v.UserID}}); err != nil {
				return jobs.ErrRetry
			}
			return rpc.Errf("conflict", "Database user create failed; reconciliation required")
		}
		return jobs.ErrRetry
	}
	return nil
}

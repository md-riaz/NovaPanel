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
	"novapanel/internal/exec"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"strings"
)

type databaseCreateArgs struct {
	SiteID *string `json:"site_id,omitempty"`
	Name   string  `json:"name,omitempty"`
}

func (v *databaseCreateArgs) Validate() error {
	if v.SiteID != nil && !id.Valid(*v.SiteID) {
		return rpc.Errf("invalid_argument", "Invalid site ID")
	}
	if v.Name != "" {
		return (&validate.V{}).DBName(v.Name)
	}
	return nil
}

type databaseJobArgs struct {
	DBID     string  `json:"db_id"`
	UserID   string  `json:"user_id"`
	SiteID   *string `json:"site_id,omitempty"`
	Name     string  `json:"name"`
	Username string  `json:"username"`
	Password string  `json:"password,omitempty"`
}

func (v *databaseJobArgs) Validate() error {
	if !id.Valid(v.DBID) || !id.Valid(v.UserID) || v.Password == "" {
		return rpc.Errf("invalid_argument", "Invalid database job")
	}
	return (&databaseCreateArgs{SiteID: v.SiteID, Name: v.Name}).Validate()
}
func init() {
	rpc.Register(rpc.Verb{Name: "database.create", MinRole: rpc.Admin, Scope: "databases:write", Slow: true, SealedInput: true, NewArgs: func() any { return &databaseCreateArgs{} }, NewJobArgs: func() any { return &databaseJobArgs{} }, JobObjectKey: func(v any) string { return "db:" + v.(*databaseJobArgs).DBID }, Run: databaseCreate, Exec: databaseCreateExec})
}
func databaseCreate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*databaseCreateArgs)
	dbid, e := id.New()
	if e != nil {
		return nil, e
	}
	uid, e := id.New()
	if e != nil {
		return nil, e
	}
	name := v.Name
	if name == "" {
		name = "np_" + strings.ToLower(dbid[6:22])
	}
	username := "np_" + strings.ToLower(uid[6:22])
	var key [24]byte
	if _, e = rand.Read(key[:]); e != nil {
		return nil, e
	}
	password := hex.EncodeToString(key[:])
	clear(key[:])
	j := databaseJobArgs{DBID: dbid, UserID: uid, SiteID: v.SiteID, Name: name, Username: username}
	d := store.Database{ID: dbid, SiteID: v.SiteID, Name: name, Engine: "mariadb"}
	u := store.DatabaseUser{ID: uid, DatabaseID: dbid, Username: username}
	result, err := a.Jobs.EnqueueDatabase(ctx, "database.create", j, "db:"+dbid, password, &d, &u)
	if errors.Is(err, store.ErrSiteConflict) {
		return nil, rpc.Errf("conflict", "Database metadata conflict")
	}
	return result, err
}
func databaseCreateExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	durable, e := a.Store.GetJob(ctx, j.ID)
	if e != nil {
		return e
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
		return rpc.Errf("bad_request", "Invalid sealed input")
	}
	defer clear(raw)
	var v databaseJobArgs
	if e = json.Unmarshal(raw, &v); e != nil {
		return rpc.Errf("bad_request", "Invalid database input")
	}
	if e = v.Validate(); e != nil {
		return e
	}
	if j.ObjectKey != "db:"+v.DBID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	unlockName, e := a.Jobs.LockDatabaseName(ctx, v.Name)
	if e != nil {
		return e
	}
	defer unlockName()
	unlock, e := a.Jobs.LockDatabaseUser(ctx, v.Username)
	if e != nil {
		return e
	}
	defer unlock()
	if strings.HasPrefix(durable.Step, "database_cleanup:") {
		// Legacy cleanup checkpoints cannot authorize an external deletion.
		return rpc.Errf("conflict", "Database cleanup requires reconciliation")
	}
	if v.SiteID != nil {
		site, err := a.Store.SiteByID(ctx, *v.SiteID)
		if errors.Is(err, sql.ErrNoRows) {
			return rpc.Errf("not_found", "Site not found")
		}
		if err != nil {
			return err
		}
		if site.Status == "deleting" || site.Status == "failed" {
			return rpc.Errf("precondition_failed", "Site is not createable")
		}
	}
	if e = databaseReady(ctx, a.Exec); e != nil {
		return e
	}
	if e = database.ManagedCreate(ctx, a.Exec, v.DBID, v.UserID, v.Name, v.Username, v.Password); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(e, database.ErrIndeterminate) {
			return jobs.ErrRetry
		}
		code := "agent_error"
		if errors.Is(e, database.ErrConflict) {
			code = "conflict"
		}
		return cleanupDatabaseCreate(ctx, a.Exec, v, code)
	}
	result, e := json.Marshal(map[string]string{"db_id": v.DBID, "db_name": v.Name, "db_user": v.Username})
	if e != nil {
		return e
	}
	if e = a.Jobs.PublishDatabaseClaim(ctx, j, result, map[string]any{"password": v.Password}); e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return cleanupDatabaseCreate(ctx, a.Exec, v, "agent_error")
	}
	return nil
}

func cleanupDatabaseCreate(ctx context.Context, runner exec.Runner, v databaseJobArgs, code string) error {
	objects := []database.AbortObject{{Kind: "user", Name: v.Username, ID: v.UserID}, {Kind: "schema", Name: v.Name, ID: v.DBID}}
	if err := database.AbortCreate(ctx, runner, objects); err != nil {
		return jobs.ErrRetry
	}
	return rpc.Errf(code, "Database create failed; reconciliation required")
}

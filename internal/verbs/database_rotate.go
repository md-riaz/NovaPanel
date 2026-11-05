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
)

type databaseIDArgs struct {
	DBID string `json:"db_id"`
}

func (v *databaseIDArgs) Validate() error {
	if !id.Valid(v.DBID) {
		return rpc.Errf("invalid_argument", "Invalid database ID")
	}
	return nil
}

type databaseRotateJob struct {
	DBID     string  `json:"db_id"`
	SiteID   *string `json:"site_id,omitempty"`
	Name     string  `json:"name"`
	UserID   string  `json:"user_id"`
	Username string  `json:"username"`
	Password string  `json:"password,omitempty"`
}

func (v *databaseRotateJob) Validate() error {
	if !id.Valid(v.DBID) || !id.Valid(v.UserID) || v.Name == "" || v.Username == "" || v.Password == "" {
		return rpc.Errf("invalid_argument", "Invalid database job")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "database.rotate_password", MinRole: rpc.Admin, Scope: "databases:write", Slow: true, MFA: true, SealedInput: true, NewArgs: func() any { return &databaseIDArgs{} }, NewJobArgs: func() any { return &databaseRotateJob{} }, JobObjectKey: func(v any) string { return "db:" + v.(*databaseRotateJob).DBID }, Run: databaseRotate, Exec: databaseRotateExec})
}
func databaseRotate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	d, users, e := a.Store.DatabaseWithUsers(ctx, args.(*databaseIDArgs).DBID)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, rpc.Errf("not_found", "Database not found")
	}
	if e != nil {
		return nil, e
	}
	var owner *store.DatabaseUser
	for i := range users {
		if users[i].Owner {
			owner = &users[i]
			break
		}
	}
	if owner == nil {
		return nil, rpc.Errf("precondition_failed", "Database has no owner")
	}
	var key [24]byte
	if _, e = rand.Read(key[:]); e != nil {
		return nil, e
	}
	pw := hex.EncodeToString(key[:])
	clear(key[:])
	v := databaseRotateJob{DBID: d.ID, SiteID: d.SiteID, Name: d.Name, UserID: owner.ID, Username: owner.Username}
	return a.Jobs.EnqueueDatabase(ctx, "database.rotate_password", v, "db:"+d.ID, pw, nil, nil)
}
func databaseRotateExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
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
	var v databaseRotateJob
	if e = json.Unmarshal(raw, &v); e != nil {
		return rpc.Errf("bad_request", "Invalid database input")
	}
	if e = v.Validate(); e != nil {
		return e
	}
	if j.ObjectKey != "db:"+v.DBID {
		return rpc.Errf("forbidden", "Job binding mismatch")
	}
	d, users, e := a.Store.DatabaseWithUsers(ctx, v.DBID)
	if e != nil {
		return e
	}
	found := false
	for _, u := range users {
		if u.Owner && u.ID == v.UserID && u.Username == v.Username {
			found = true
		}
	}
	if d.Name != v.Name || !found {
		return rpc.Errf("precondition_failed", "Database state changed")
	}
	unlock, e := a.Jobs.LockDatabaseUser(ctx, v.Username)
	if e != nil {
		return e
	}
	defer unlock()
	if e = databaseReady(ctx, a.Exec); e != nil {
		return e
	}
	if e = database.ManagedRotate(ctx, a.Exec, v.Username, v.UserID, v.Password); e != nil {
		if errors.Is(e, database.ErrConflict) {
			return rpc.Errf("conflict", "Database user provenance conflict")
		}
		return e
	}
	if e = a.Jobs.PublishDatabaseClaim(ctx, j, json.RawMessage(`{}`), map[string]any{"password": v.Password}); e != nil {
		// The external password has changed. Keep the sealed input for roll-forward;
		// publishing the claim and its checkpoint is atomic.
		return jobs.ErrRetry
	}
	return nil
}

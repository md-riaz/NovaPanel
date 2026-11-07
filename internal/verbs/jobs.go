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
	"novapanel/internal/store"
)

type jobIDArgs struct {
	ID string `json:"id"`
}

func (a *jobIDArgs) Validate() error {
	if a.ID == "" {
		return rpc.Errf("invalid_argument", "Job id required")
	}
	return nil
}

type claimArgs struct {
	JobID string `json:"job_id"`
	Claim string `json:"claim"`
}

func (a *claimArgs) Validate() error {
	if a.JobID == "" || a.Claim == "" {
		return rpc.Errf("invalid_argument", "Job id and claim required")
	}
	return nil
}

type jobListArgs struct {
	State  string `json:"state,omitempty"`
	Verb   string `json:"verb,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Before string `json:"before,omitempty"`
}

func (a *jobListArgs) Validate() error {
	if a.Limit < 0 || a.Limit > 200 {
		return rpc.Errf("invalid_argument", "Invalid limit")
	}
	return nil
}

type reconcileArgs struct {
	Scope string `json:"scope,omitempty"`
}

func (a *reconcileArgs) Validate() error {
	if a.Scope != "" && a.Scope != "database" {
		return rpc.Errf("invalid_argument", "Unsupported reconcile scope")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "job.status", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &jobIDArgs{} }, Run: jobStatus})
	rpc.Register(rpc.Verb{Name: "job.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &jobListArgs{} }, Run: jobList})
	rpc.Register(rpc.Verb{Name: "job.claim", MinRole: rpc.ReadOnly, NewArgs: func() any { return &claimArgs{} }, Run: jobClaim})
	rpc.Register(rpc.Verb{Name: "reconcile", MinRole: rpc.Owner, Slow: true, Effectful: true, NewArgs: func() any { return &reconcileArgs{} }, ObjectKey: func(any) string { return "system" }, Run: reconcile, Exec: reconcileExec})
}
func missingJob(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return rpc.Errf("not_found", "Job not found")
	}
	return e
}
func jobStatus(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if a.Jobs == nil {
		return nil, rpc.Errf("unsupported", "Queue unavailable")
	}
	id := args.(*jobIDArgs).ID
	j, e := a.Jobs.Status(ctx, id)
	if e != nil {
		return nil, missingJob(e)
	}
	events, e := a.Store.JobEvents(ctx, id)
	if e != nil {
		return nil, e
	}
	lines := []map[string]any{}
	for _, ev := range events {
		lines = append(lines, map[string]any{"ts": ev.TS, "msg": ev.Msg})
	}
	return struct {
		store.Job
		Events []map[string]any `json:"events"`
	}{j, lines}, nil
}
func jobList(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if a.Jobs == nil {
		return nil, rpc.Errf("unsupported", "Queue unavailable")
	}
	x := args.(*jobListArgs)
	limit := x.Limit
	if limit == 0 {
		limit = 50
	}
	rows, e := a.Jobs.List(ctx, store.JobFilter{State: x.State, Verb: x.Verb, Limit: limit, Before: x.Before})
	if e != nil {
		return nil, e
	}
	next := ""
	if len(rows) == limit {
		next = rows[len(rows)-1].ID
	}
	return struct {
		Jobs []store.Job `json:"jobs"`
		Next string      `json:"next,omitempty"`
	}{rows, next}, nil
}
func jobClaim(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if a.Jobs == nil {
		return nil, rpc.Errf("unsupported", "Queue unavailable")
	}
	x := args.(*claimArgs)
	fields, e := a.Jobs.Claim(ctx, x.JobID, x.Claim)
	return fields, missingJob(e)
}
func reconcile(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return a.Jobs.Enqueue(ctx, "reconcile", args, "system")
}
func reconcileExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var args reconcileArgs
	if len(j.Args) != 0 {
		if err := json.Unmarshal(j.Args, &args); err != nil {
			return err
		}
	}
	if err := args.Validate(); err != nil {
		return err
	}
	changed, err := database.Reconcile(ctx, a.Exec, a.Store, a.Jobs.LockReconcileDatabase)
	if args.Scope == "" {
		phpChanged, phpErr := reconcilePHP(ctx, a.Store, a.Jobs, a.Exec, a.PHP, a.Cfg.SitesRoot, a.Cfg.LogDir)
		changed = append(changed, phpChanged...)
		err = errors.Join(err, phpErr)
	}
	result, marshalErr := json.Marshal(struct {
		Changed []string `json:"changed"`
	}{changed})
	if marshalErr != nil {
		return marshalErr
	}
	j.Result = result
	if errors.Is(err, database.ErrConflict) {
		return rpc.Errf("conflict", "Provenance reconciliation requires operator attention")
	}
	return err
}

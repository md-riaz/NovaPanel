package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type jobIDArgs struct {
	ID string `json:"id"`
}

func (a *jobIDArgs) Validate() error {
	if !id.Valid(a.ID) {
		return rpc.Errf("invalid_argument", "Invalid id")
	}
	return nil
}

type claimArgs struct {
	ID    string `json:"id"`
	Claim string `json:"claim"`
}

func (a *claimArgs) Validate() error {
	if !id.Valid(a.ID) || a.Claim == "" {
		return rpc.Errf("invalid_argument", "Invalid claim")
	}
	return nil
}

type listJobsArgs struct {
	State  string `json:"state"`
	Verb   string `json:"verb"`
	Before string `json:"before"`
	Limit  int    `json:"limit"`
}

func (a *listJobsArgs) Validate() error {
	if a.Limit < 0 || a.Limit > 200 || (a.Before != "" && !id.Valid(a.Before)) {
		return rpc.Errf("invalid_argument", "Invalid pagination")
	}
	switch a.State {
	case "", "queued", "running", "done", "failed":
	default:
		return rpc.Errf("invalid_argument", "Invalid state")
	}
	return nil
}

type scheduleArgs struct {
	ID      string          `json:"id,omitempty"`
	Verb    string          `json:"verb"`
	Cron    string          `json:"cron"`
	Args    json.RawMessage `json:"args"`
	Enabled *bool           `json:"enabled,omitempty"`
}

func (a *scheduleArgs) Validate() error {
	if a.ID != "" && !id.Valid(a.ID) {
		return rpc.Errf("invalid_argument", "Invalid id")
	}
	return nil
}
func queueReady(a agent.Agent) error {
	if a.Jobs == nil || a.Scheduler == nil {
		return rpc.Errf("unsupported", "Jobs unavailable")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "job.status", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &jobIDArgs{} }, Run: func(ctx context.Context, a *agent.Agent, arg any) (any, error) {
		if e := queueReady(*a); e != nil {
			return nil, e
		}
		return a.Jobs.Status(ctx, arg.(*jobIDArgs).ID)
	}})
	rpc.Register(rpc.Verb{Name: "job.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &listJobsArgs{} }, Run: func(ctx context.Context, a *agent.Agent, arg any) (any, error) {
		if e := queueReady(*a); e != nil {
			return nil, e
		}
		p := arg.(*listJobsArgs)
		return a.Jobs.List(ctx, store.JobFilter{State: p.State, Verb: p.Verb, Before: p.Before, Limit: p.Limit})
	}})
	rpc.Register(rpc.Verb{Name: "job.claim", MinRole: rpc.ReadOnly, NewArgs: func() any { return &claimArgs{} }, Run: func(ctx context.Context, a *agent.Agent, arg any) (any, error) {
		if e := queueReady(*a); e != nil {
			return nil, e
		}
		p := arg.(*claimArgs)
		return a.Jobs.Claim(ctx, p.ID, p.Claim)
	}})
	rpc.Register(rpc.Verb{Name: "reconcile", MinRole: rpc.Owner, Slow: true, Effectful: true, NewArgs: func() any { return &emptyArgs{} }, ObjectKey: func(any) string { return "system" }, Run: enqueue("reconcile", func(any) string { return "system" }), Exec: func(context.Context, *agent.Agent, *jobs.Job) error { return nil }})
	rpc.Register(rpc.Verb{Name: "schedule.list", MinRole: rpc.Owner, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &emptyArgs{} }, Run: func(ctx context.Context, a *agent.Agent, _ any) (any, error) {
		if e := queueReady(*a); e != nil {
			return nil, e
		}
		return a.Scheduler.List(ctx)
	}})
	rpc.Register(rpc.Verb{Name: "schedule.set", MinRole: rpc.Owner, Scope: "system:write", NewArgs: func() any { return &scheduleArgs{} }, Run: func(ctx context.Context, a *agent.Agent, arg any) (any, error) {
		if e := queueReady(*a); e != nil {
			return nil, e
		}
		p := arg.(*scheduleArgs)
		return a.Scheduler.Set(ctx, jobs.ScheduleRequest{ID: p.ID, Verb: p.Verb, Cron: p.Cron, Args: p.Args, Enabled: p.Enabled})
	}})
	rpc.Register(rpc.Verb{Name: "schedule.enable", MinRole: rpc.Owner, Scope: "system:write", NewArgs: func() any { return &jobIDArgs{} }, Run: func(ctx context.Context, a *agent.Agent, arg any) (any, error) {
		if e := queueReady(*a); e != nil {
			return nil, e
		}
		e := a.Scheduler.Enable(ctx, arg.(*jobIDArgs).ID, true)
		return struct{}{}, e
	}})
	rpc.Register(rpc.Verb{Name: "schedule.disable", MinRole: rpc.Owner, Scope: "system:write", NewArgs: func() any { return &jobIDArgs{} }, Run: func(ctx context.Context, a *agent.Agent, arg any) (any, error) {
		if e := queueReady(*a); e != nil {
			return nil, e
		}
		e := a.Scheduler.Enable(ctx, arg.(*jobIDArgs).ID, false)
		return struct{}{}, e
	}})
}
func enqueue(verb string, key func(any) string) func(context.Context, *agent.Agent, any) (any, error) {
	return func(ctx context.Context, a *agent.Agent, args any) (any, error) {
		if a.Jobs == nil {
			return nil, rpc.Errf("unsupported", "Job queue unavailable")
		}
		return a.Jobs.Enqueue(ctx, verb, args, key(args))
	}
}

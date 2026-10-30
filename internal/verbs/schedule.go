package verbs

import (
	"context"
	"encoding/json"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type scheduleSetArgs struct {
	ID      string          `json:"id,omitempty"`
	Verb    string          `json:"verb"`
	Args    json.RawMessage `json:"args"`
	Cron    string          `json:"cron"`
	Enabled *bool           `json:"enabled,omitempty"`
}
type scheduleIDArgs struct {
	ID string `json:"id"`
}

func (a *scheduleIDArgs) Validate() error {
	if a.ID == "" {
		return rpc.Errf("invalid_argument", "Schedule id required")
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "schedule.list", MinRole: rpc.Admin, ReadOnly: true, Scope: "system:read", NewArgs: func() any { return &emptyArgs{} }, Run: scheduleList})
	rpc.Register(rpc.Verb{Name: "schedule.set", MinRole: rpc.Admin, Scope: "system:write", NewArgs: func() any { return &scheduleSetArgs{} }, Run: scheduleSet})
	rpc.Register(rpc.Verb{Name: "schedule.enable", MinRole: rpc.Admin, Scope: "system:write", NewArgs: func() any { return &scheduleIDArgs{} }, Run: scheduleEnable})
	rpc.Register(rpc.Verb{Name: "schedule.disable", MinRole: rpc.Admin, Scope: "system:write", NewArgs: func() any { return &scheduleIDArgs{} }, Run: scheduleDisable})
}
func scheduleList(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	if a.Scheduler == nil {
		return nil, rpc.Errf("unsupported", "Scheduler unavailable")
	}
	rows, e := a.Scheduler.List(ctx)
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, map[string]any{"id": r.ID, "verb": r.Verb, "cron": r.Cron, "next_run": r.NextRun, "enabled": r.Enabled, "last_run": r.LastRun})
	}
	return map[string]any{"schedules": out}, nil
}
func scheduleSet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if a.Scheduler == nil {
		return nil, rpc.Errf("unsupported", "Scheduler unavailable")
	}
	x := args.(*scheduleSetArgs)
	if e := rpc.AuthorizeSchedule(ctx, x.Verb); e != nil {
		return nil, e
	}
	row, e := a.Scheduler.Set(ctx, jobs.ScheduleRequest{ID: x.ID, Verb: x.Verb, Args: x.Args, Cron: x.Cron, Enabled: x.Enabled})
	if e != nil {
		return nil, e
	}
	return map[string]string{"id": row.ID}, nil
}
func scheduleEnable(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return setScheduleEnabled(ctx, a.Scheduler, a.Store, args.(*scheduleIDArgs).ID, true)
}
func scheduleDisable(ctx context.Context, a *agent.Agent, args any) (any, error) {
	return setScheduleEnabled(ctx, a.Scheduler, a.Store, args.(*scheduleIDArgs).ID, false)
}
func setScheduleEnabled(ctx context.Context, scheduler *jobs.Scheduler, st *store.Store, id string, enabled bool) (any, error) {
	if scheduler == nil {
		return nil, rpc.Errf("unsupported", "Scheduler unavailable")
	}
	if enabled {
		row, e := st.GetSchedule(ctx, id)
		if e != nil {
			return nil, missingJob(e)
		}
		if e = rpc.AuthorizeSchedule(ctx, row.Verb); e != nil {
			return nil, e
		}
	}
	e := scheduler.Enable(ctx, id, enabled)
	return struct{}{}, missingJob(e)
}

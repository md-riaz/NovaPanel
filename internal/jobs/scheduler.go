package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/store"
	"sync"
	"time"
)

type SchedulePolicy struct {
	Effectful, RequiresMFA bool
	ObjectKey              func(json.RawMessage) (string, error)
}
type ScheduleRequest struct {
	ID, Verb, Cron string
	Args           json.RawMessage
	Enabled        *bool
}
type Scheduler struct {
	q        *Queue
	clock    Clock
	policies map[string]SchedulePolicy
	mu       sync.Mutex
}

func NewScheduler(q *Queue, c Clock, policies map[string]SchedulePolicy) *Scheduler {
	copy := make(map[string]SchedulePolicy, len(policies))
	for k, v := range policies {
		copy[k] = v
	}
	if c == nil {
		c = realClock{}
	}
	return &Scheduler{q: q, clock: c, policies: copy}
}
func (s *Scheduler) policy(verb string) (SchedulePolicy, error) {
	switch verb {
	case "ssl.renew", "backup.create", "backup.verify", "update.check", "reconcile", "site.cron":
	default:
		return SchedulePolicy{}, rpcerr.Errf("invalid_argument", "verb cannot be scheduled")
	}
	p, ok := s.policies[verb]
	if !ok {
		return p, rpcerr.Errf("invalid_argument", "schedulable verb not registered")
	}
	return p, nil
}
func scheduleOwner(ctx context.Context) (Metadata, error) {
	m, ok := MetadataFrom(ctx)
	if !ok || m.Actor == "" || m.CreatedBy == "" || (m.Role != "owner" && m.Role != "admin") {
		return m, rpcerr.Errf("forbidden", "schedule owner required")
	}
	return m, nil
}
func (s *Scheduler) Set(ctx context.Context, r ScheduleRequest) (store.Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := scheduleOwner(ctx)
	if e != nil {
		return store.Schedule{}, e
	}
	p, e := s.policy(r.Verb)
	if e != nil {
		return store.Schedule{}, e
	}
	if p.RequiresMFA && !m.Automation {
		return store.Schedule{}, rpcerr.Errf("mfa_required", "automation grant required")
	}
	if len(r.Args) == 0 {
		r.Args = json.RawMessage(`{}`)
	}
	if !json.Valid(r.Args) {
		return store.Schedule{}, rpcerr.Errf("invalid_argument", "invalid schedule args")
	}
	if p.ObjectKey != nil {
		if _, e = p.ObjectKey(r.Args); e != nil {
			return store.Schedule{}, e
		}
	}
	next, e := NextCalendar(r.Cron, s.clock.Now())
	if e != nil {
		return store.Schedule{}, e
	}
	row := store.Schedule{ID: r.ID, Verb: r.Verb, Args: r.Args, Cron: r.Cron, NextRun: next, Enabled: true, Actor: m.Actor, Role: m.Role, CreatedBy: m.CreatedBy, Scopes: m.Scopes, Automation: m.Automation, State: json.RawMessage(`{}`)}
	if r.Enabled != nil {
		row.Enabled = *r.Enabled
	}
	if row.ID == "" {
		row.ID, e = id.New()
		if e != nil {
			return row, e
		}
	} else {
		old, err := s.q.store.GetSchedule(ctx, row.ID)
		if err != nil {
			return row, err
		}
		if old.CreatedBy != m.CreatedBy {
			return row, rpcerr.Errf("forbidden", "schedule belongs to another owner")
		}
		row.LastRun = old.LastRun
		row.State = old.State
	}
	if e = s.q.record(Job{ID: row.ID, Verb: "schedule.set", Actor: m.Actor, Role: m.Role, ObjectKey: row.ID}, "authorized", ""); e != nil {
		return row, e
	}
	e = s.q.store.PutSchedule(ctx, row)
	return row, e
}
func (s *Scheduler) List(ctx context.Context) ([]store.Schedule, error) {
	return s.q.store.ListSchedules(ctx)
}
func (s *Scheduler) Enable(ctx context.Context, jid string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, e := scheduleOwner(ctx)
	if e != nil {
		return e
	}
	row, e := s.q.store.GetSchedule(ctx, jid)
	if e != nil {
		return e
	}
	if row.CreatedBy != m.CreatedBy {
		return rpcerr.Errf("forbidden", "schedule belongs to another owner")
	}
	if _, e = s.policy(row.Verb); e != nil {
		return e
	}
	if e = s.q.record(Job{ID: jid, Verb: "schedule.enable", Actor: m.Actor, Role: m.Role, ObjectKey: jid}, "authorized", ""); e != nil {
		return e
	}
	return s.q.store.SetScheduleEnabled(ctx, jid, enabled)
}

// Tick reserves cadence with a durable CAS before enqueue. It never waits for a
// job's executor or holds a SQL transaction while performing an external action.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	rows, e := s.q.store.DueSchedules(ctx, now)
	if e != nil {
		return e
	}
	for _, row := range rows {
		p, e := s.policy(row.Verb)
		if e != nil {
			return e
		}
		if (row.Role != "owner" && row.Role != "admin") || row.Actor == "" || row.CreatedBy == "" {
			return rpcerr.Errf("forbidden", "invalid stored schedule owner")
		}
		if p.RequiresMFA && !row.Automation {
			return rpcerr.Errf("mfa_required", "stored automation grant required")
		}
		state := map[string]any{}
		if len(row.State) > 0 {
			if e = json.Unmarshal(row.State, &state); e != nil {
				return e
			}
		}
		if previous, ok := state["job_id"].(string); ok && previous != "" {
			j, err := s.q.Status(ctx, previous)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && (j.State == "queued" || j.State == "running") {
				continue
			}
		}
		key := "schedule:" + row.ID
		if p.ObjectKey != nil {
			key, e = p.ObjectKey(row.Args)
			if e != nil {
				return e
			}
		}
		// Other schedules and manually enqueued jobs sharing an object also
		// suppress this firing while queued/running, not just this row's job.
		busy := false
		for _, phase := range []string{"queued", "running"} {
			before := ""
			for {
				pending, err := s.q.List(ctx, store.JobFilter{State: phase, Before: before, Limit: 200})
				if err != nil {
					return err
				}
				for _, j := range pending {
					if j.ObjectKey == key {
						busy = true
						break
					}
				}
				if busy || len(pending) < 200 {
					break
				}
				before = pending[len(pending)-1].ID
			}
			if busy {
				break
			}
		}
		if busy {
			continue
		}
		next, e := NextCalendar(row.Cron, now)
		if e != nil {
			return e
		}
		m := Metadata{Actor: row.Actor, Role: row.Role, CreatedBy: row.CreatedBy, Scopes: row.Scopes, Automation: row.Automation, Effectful: p.Effectful}
		if e = s.q.record(Job{ID: row.ID, Verb: row.Verb, ObjectKey: key, Actor: m.Actor, Role: m.Role}, "authorized", ""); e != nil {
			return e
		}
		if e = s.q.store.AdvanceSchedule(ctx, row.ID, row.NextRun, next, now, row.State); errors.Is(e, sql.ErrNoRows) {
			continue
		} else if e != nil {
			return e
		}
		result, enqueueErr := s.q.Enqueue(WithMetadata(ctx, m), row.Verb, row.Args, key)
		if enqueueErr != nil {
			// Retry due work after recoverable persistence errors instead of losing cadence.
			rollbackErr := s.q.store.AdvanceSchedule(ctx, row.ID, next, row.NextRun, now, row.State)
			return errors.Join(enqueueErr, rollbackErr)
		}
		state["job_id"] = result.Job.ID
		state["object_key"] = key
		raw, e := json.Marshal(state)
		if e != nil {
			return e
		}
		if e = s.q.store.AdvanceSchedule(ctx, row.ID, next, next, now, raw); e != nil {
			return e
		}
	}
	return s.q.store.DeleteExpiredClaims(ctx, now)
}
func (s *Scheduler) Run(ctx context.Context) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if e := s.Tick(ctx); e != nil {
			return e
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

package jobs

import (
	"context"
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
	// The site-level cron surface is deferred to Phase 5, not guessed here.
	case "ssl.renew", "backup.create", "backup.verify", "update.check", "reconcile":
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
	if !ok || m.CreatedBy == "" {
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
		return store.Schedule{}, rpcerr.Errf("forbidden", "automation grant required")
	}
	if len(r.Args) == 0 {
		r.Args = json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(r.Args, &object) != nil || object == nil {
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
		row.LastRun = old.LastRun
		row.State = old.State
	}
	if e = s.q.healthy(); e != nil {
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
	_, e := scheduleOwner(ctx)
	if e != nil {
		return e
	}
	row, e := s.q.store.GetSchedule(ctx, jid)
	if e != nil {
		return e
	}
	if _, e = s.policy(row.Verb); e != nil {
		return e
	}
	if e = s.q.healthy(); e != nil {
		return e
	}
	return s.q.store.SetScheduleEnabled(ctx, jid, enabled)
}

// Tick delegates cadence CAS, busy-object skip and job insertion to one store transaction.
func (s *Scheduler) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.q.healthy(); e != nil {
		return e
	}
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
		if p.RequiresMFA && !row.Automation {
			return rpcerr.Errf("forbidden", "stored automation grant required")
		}
		next, e := NextCalendar(row.Cron, now)
		if e != nil {
			return e
		}
		key := "schedule:" + row.ID
		if p.ObjectKey != nil {
			key, e = p.ObjectKey(row.Args)
			if e != nil {
				return e
			}
		}
		state := map[string]any{}
		if len(row.State) > 0 {
			if e = json.Unmarshal(row.State, &state); e != nil {
				return e
			}
		}
		if state == nil {
			state = map[string]any{}
		}
		jid, e := id.New()
		if e != nil {
			return e
		}
		j := Job{ID: jid, Verb: row.Verb, Args: row.Args, ObjectKey: key, Actor: row.Actor, Role: row.Role, CreatedBy: row.CreatedBy, Scopes: row.Scopes, Automation: row.Automation, Effectful: p.Effectful, State: "queued", Created: now, Updated: now}
		state["job_id"], state["object_key"] = jid, key
		raw, e := json.Marshal(state)
		if e != nil {
			return e
		}
		enqueued, e := s.q.store.EnqueueScheduledJob(ctx, row.ID, row.NextRun, next, now, raw, j, nil)
		if e != nil {
			return e
		}
		if !enqueued {
			continue
		}
		if e = s.q.record(j, "ok", "", "enqueue"); e != nil {
			return e
		}
		select {
		case s.q.wake <- struct{}{}:
		default:
		}
	}
	return s.q.store.DeleteExpiredClaims(ctx)
}
func (s *Scheduler) Run(ctx context.Context) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if e := s.Tick(ctx); e != nil {
			if ctx.Err() != nil {
				return nil
			}
			if health := s.q.healthy(); health == nil || !errors.Is(e, health) {
				return e
			}
			// Sticky audit failure pauses mutations, not read RPC or cancellation.
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

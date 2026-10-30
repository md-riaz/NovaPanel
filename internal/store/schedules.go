package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type Schedule struct {
	ID                     string          `json:"id"`
	Verb                   string          `json:"verb"`
	Cron                   string          `json:"cron"`
	Args                   json.RawMessage `json:"args"`
	State                  json.RawMessage `json:"state"`
	NextRun                int64           `json:"next_run"`
	Enabled                bool            `json:"enabled"`
	LastRun                *int64          `json:"last_run,omitempty"`
	Actor, Role, CreatedBy string          `json:"-"`
	Scopes                 []string        `json:"-"`
	Automation             bool            `json:"-"`
}

const scheduleColumns = "id,verb,args_json,cron,state_json,next_run,enabled,last_run,actor,role,owner_id,scopes_json,automation"

func scanSchedule(r scanner) (v Schedule, e error) {
	var a, st, sc string
	e = r.Scan(&v.ID, &v.Verb, &a, &v.Cron, &st, &v.NextRun, &v.Enabled, &v.LastRun, &v.Actor, &v.Role, &v.CreatedBy, &sc, &v.Automation)
	if e != nil {
		return
	}
	v.Args = json.RawMessage(a)
	v.State = json.RawMessage(st)
	e = json.Unmarshal([]byte(sc), &v.Scopes)
	return
}
func (s *Store) PutSchedule(ctx context.Context, v Schedule) error {
	if v.Scopes == nil {
		v.Scopes = []string{}
	}
	sc, e := json.Marshal(v.Scopes)
	if e != nil {
		return e
	}
	if len(v.State) == 0 {
		v.State = json.RawMessage(`{}`)
	}
	_, e = s.db.ExecContext(ctx, `INSERT INTO schedules(id,verb,args_json,cron,state_json,next_run,enabled,last_run,actor,role,owner_id,scopes_json,automation) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET verb=excluded.verb,args_json=excluded.args_json,cron=excluded.cron,state_json=excluded.state_json,next_run=excluded.next_run,enabled=excluded.enabled,last_run=excluded.last_run,actor=excluded.actor,role=excluded.role,owner_id=excluded.owner_id,scopes_json=excluded.scopes_json,automation=excluded.automation`, v.ID, v.Verb, string(v.Args), v.Cron, string(v.State), v.NextRun, v.Enabled, v.LastRun, v.Actor, v.Role, v.CreatedBy, string(sc), v.Automation)
	return e
}
func (s *Store) GetSchedule(ctx context.Context, id string) (Schedule, error) {
	return scanSchedule(s.db.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id=?", id))
}
func (s *Store) readSchedules(ctx context.Context, q string, args ...any) ([]Schedule, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT "+scheduleColumns+" FROM schedules "+q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		v, e := scanSchedule(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) ListSchedules(ctx context.Context) ([]Schedule, error) {
	return s.readSchedules(ctx, "ORDER BY id")
}
func (s *Store) DueSchedules(ctx context.Context, now int64) ([]Schedule, error) {
	return s.readSchedules(ctx, "WHERE enabled=1 AND next_run<=? ORDER BY next_run,id", now)
}
func (s *Store) SetScheduleEnabled(ctx context.Context, id string, enabled bool) error {
	r, e := s.db.ExecContext(ctx, "UPDATE schedules SET enabled=? WHERE id=?", enabled, id)
	return affected(r, e)
}
func (s *Store) AdvanceSchedule(ctx context.Context, id string, expectedNext, next, now int64, state json.RawMessage) error {
	r, e := s.db.ExecContext(ctx, "UPDATE schedules SET next_run=?,last_run=?,state_json=? WHERE id=? AND next_run=? AND enabled=1", next, now, string(state), id, expectedNext)
	return affected(r, e)
}

type Event struct {
	ID, Actor, Role, Verb, ObjectKey, Outcome, Code string
	TS                                              int64
	Detail                                          json.RawMessage
}

func (s *Store) InsertEvent(ctx context.Context, v Event) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO events(id,ts,actor,role,verb,object_key,outcome,code,detail_json) VALUES(?,?,?,?,?,?,?,?,?)", v.ID, v.TS, v.Actor, v.Role, v.Verb, v.ObjectKey, v.Outcome, v.Code, nullableJSON(v.Detail))
	return e
}
func (s *Store) ListEvents(ctx context.Context, limit int, before string) ([]Event, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	q := "SELECT id,ts,actor,coalesce(role,''),verb,coalesce(object_key,''),outcome,coalesce(code,''),coalesce(detail_json,'{}') FROM events"
	args := []any{}
	if before != "" {
		q += " WHERE id<?"
		args = append(args, before)
	}
	q += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, e := s.db.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var v Event
		var d string
		if e = rows.Scan(&v.ID, &v.TS, &v.Actor, &v.Role, &v.Verb, &v.ObjectKey, &v.Outcome, &v.Code, &d); e != nil {
			return nil, e
		}
		v.Detail = json.RawMessage(d)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SetSetting(ctx context.Context, key string, value json.RawMessage) error {
	_, e := s.db.ExecContext(ctx, "INSERT INTO settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json", key, string(value))
	return e
}
func (s *Store) Setting(ctx context.Context, key string) (json.RawMessage, error) {
	var v string
	e := s.db.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", key).Scan(&v)
	return json.RawMessage(v), e
}

// ReplayEvent rolls a file-only record forward; a conflicting ID is corruption.
func (s *Store) ReplayEvent(ctx context.Context, v Event) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var got Event
		var detail sql.NullString
		e := tx.QueryRowContext(ctx, "SELECT id,ts,actor,coalesce(role,''),verb,coalesce(object_key,''),outcome,coalesce(code,''),detail_json FROM events WHERE id=?", v.ID).Scan(&got.ID, &got.TS, &got.Actor, &got.Role, &got.Verb, &got.ObjectKey, &got.Outcome, &got.Code, &detail)
		if errors.Is(e, sql.ErrNoRows) {
			_, e = tx.ExecContext(ctx, "INSERT INTO events(id,ts,actor,role,verb,object_key,outcome,code,detail_json) VALUES(?,?,?,?,?,?,?,?,?)", v.ID, v.TS, v.Actor, v.Role, v.Verb, v.ObjectKey, v.Outcome, v.Code, nullableJSON(v.Detail))
			return e
		}
		if e != nil {
			return e
		}
		if got.ID != v.ID || got.TS != v.TS || got.Actor != v.Actor || got.Role != v.Role || got.Verb != v.Verb || got.ObjectKey != v.ObjectKey || got.Outcome != v.Outcome || got.Code != v.Code || !bytes.Equal([]byte(detail.String), v.Detail) {
			return errors.New("audit event ID conflict")
		}
		return nil
	})
}

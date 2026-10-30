package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"novapanel/internal/audit"
	"novapanel/internal/jobs"
	"slices"
)

func auditOutcome(s string) string {
	if s == "authorized" || s == "queued" {
		return "ok"
	}
	return s
}

type jobAudit struct{ *audit.Log }

func (a jobAudit) Record(e audit.Event) error {
	if e.Detail == nil {
		e.Detail = map[string]any{}
	}
	e.Detail["stage"] = e.Outcome
	e.Outcome = auditOutcome(e.Outcome)
	return a.Log.Record(e)
}

// ConfigureJobs installs the trusted worker boundary after durable state startup.
// Only reconcile is schedulable in Phase 1.
func (s *Server) ConfigureJobs() {
	a := s.Agent
	if a.Store == nil || a.Secret == nil || a.Audit == nil {
		return
	}
	a.Jobs = jobs.NewQueue(a.Store, a.Secret, jobAudit{a.Audit})
	a.Jobs.SetRunner(s.ExecuteJob)
	policies := map[string]jobs.SchedulePolicy{}
	if v, ok := s.Registry["reconcile"]; ok && v.Slow && v.Exec != nil {
		policies[v.Name] = jobs.SchedulePolicy{Effectful: v.Effectful, RequiresMFA: v.MFA, ObjectKey: func(raw json.RawMessage) (string, error) {
			args, e := decodeJobArgs(v, raw)
			if e != nil {
				return "", e
			}
			if v.ObjectKey != nil {
				return v.ObjectKey(args), nil
			}
			return "", nil
		}}
	}
	a.Scheduler = jobs.NewScheduler(a.Jobs, a.Clock, policies)
}
func decodeJobArgs(v Verb, raw json.RawMessage) (any, error) {
	if !object(raw) {
		return nil, Errf("invalid_argument", "Object args required")
	}
	args := v.NewArgs()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(args); e != nil {
		return nil, Errf("invalid_argument", "Invalid job args")
	}
	if validator, ok := args.(interface{ Validate() error }); ok {
		if e := validator.Validate(); e != nil {
			return nil, e
		}
	}
	return args, nil
}

// ExecuteJob reauthorizes persisted authority against the current registry.
// Request JSON cannot select a caller, effect classification, or executor.
func (s *Server) ExecuteJob(ctx context.Context, j *jobs.Job) error {
	m, ok := jobs.MetadataFrom(ctx)
	if !ok || m.CreatedBy == "" || m.Actor == "" || m.CreatedBy != j.CreatedBy || m.Role != j.Role {
		return Errf("forbidden", "Trusted job authority required")
	}
	v, ok := s.Registry[j.Verb]
	if !ok || !v.Slow || v.Exec == nil || v.Module != "" || v.Internal || v.Effectful != j.Effectful {
		return Errf("forbidden", "Job executor unavailable")
	}
	if m.Role == "token" {
		if v.Scope == "" || (!slices.Contains(m.Scopes, v.Scope) && !slices.Contains(m.Scopes, "*")) {
			return Errf("forbidden", "Job scope refused")
		}
	} else {
		rank, known := map[string]Role{"read_only": ReadOnly, "admin": Admin, "owner": Owner}[m.Role]
		if !known || rank < v.MinRole || (rank == ReadOnly && !v.ReadOnly) {
			return Errf("forbidden", "Job role refused")
		}
	}
	if v.MFA && !m.Automation {
		return Errf("forbidden", "Automation grant required")
	}
	args, e := decodeJobArgs(v, j.Args)
	if e != nil {
		return e
	}
	if v.ObjectKey != nil && v.ObjectKey(args) != j.ObjectKey {
		return Errf("forbidden", "Job object mismatch")
	}
	if s.DurableAudit == nil {
		return Errf("agent_error", "Durable audit unavailable")
	}
	if e = s.DurableAudit(AuditEvent{Actor: m.Actor, Role: m.Role, Verb: j.Verb, ObjectKey: j.ObjectKey, Outcome: "authorized"}); e != nil {
		return e
	}
	caller := Caller{Actor: m.Actor, Role: m.Role, UserID: m.CreatedBy, Scopes: m.Scopes, Kind: "job"}
	return v.Exec(WithCaller(ctx, caller), s.Agent, j)
}

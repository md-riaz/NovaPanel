package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"novapanel/internal/jobs"
	"slices"
)

// ExecuteJob authorizes only durable authority, never a live session or argument label.
func (s *Server) ExecuteJob(ctx context.Context, j *jobs.Job) error {
	v, ok := s.Registry[j.Verb]
	if !ok || !v.Slow || v.Exec == nil || v.Module != "" {
		return Errf("unsupported", "Job verb unavailable")
	}
	if j.CreatedBy == "" {
		return Errf("forbidden", "Job identity required")
	}
	if len(j.Scopes) > 0 && j.CreatedBy != "local-root" {
		if v.Scope == "" || (!slices.Contains(j.Scopes, v.Scope) && !slices.Contains(j.Scopes, "*")) {
			return Errf("forbidden", "Stored scope denied")
		}
	} else {
		rank, ok := map[string]Role{"read_only": ReadOnly, "admin": Admin, "owner": Owner}[j.Role]
		if !ok || (rank < v.MinRole && !(rank == ReadOnly && v.ReadOnly)) || (rank == ReadOnly && !v.ReadOnly) {
			return Errf("forbidden", "Stored role denied")
		}
	}
	if v.MFA && !j.Automation {
		return Errf("forbidden", "Stored automation grant required")
	}
	args := v.NewArgs()
	d := json.NewDecoder(bytes.NewReader(j.Args))
	d.DisallowUnknownFields()
	if d.Decode(args) != nil {
		return Errf("bad_request", "Invalid stored arguments")
	}
	if validator, ok := args.(interface{ Validate() error }); ok {
		if e := validator.Validate(); e != nil {
			return e
		}
	}
	if v.ObjectKey != nil && v.ObjectKey(args) != j.ObjectKey {
		return Errf("forbidden", "Stored object key mismatch")
	}
	if j.Effectful != v.Effectful {
		return Errf("forbidden", "Stored effect class mismatch")
	}
	return v.Exec(ctx, s.Agent, j)
}

type reauthError struct{ *CodedError }

func (e *reauthError) Unwrap() error { return e.CodedError }

func AuthorizeSchedule(ctx context.Context, name string) error {
	v, ok := Registry[name]
	if !ok {
		return Errf("invalid_argument", "Scheduled verb unavailable")
	}
	c := CallerFrom(ctx)
	if c.Kind == "token" || c.Role == "token" {
		if v.Scope == "" || (!slices.Contains(c.Scopes, v.Scope) && !slices.Contains(c.Scopes, "*")) {
			return Errf("forbidden", "Scheduled scope denied")
		}
	} else {
		rank, ok := map[string]Role{"read_only": ReadOnly, "admin": Admin, "owner": Owner}[c.Role]
		if !ok || (rank < v.MinRole && !(rank == ReadOnly && v.ReadOnly)) || (rank == ReadOnly && !v.ReadOnly) {
			return Errf("forbidden", "Scheduled role denied")
		}
	}
	if v.MFA && !c.Automation {
		return &reauthError{Errf("forbidden", "Reauthentication required for automation")}
	}
	return nil
}

// ConfigureJobs installs registry-derived scheduler policy and the worker boundary.
func (s *Server) ConfigureJobs() {
	a := s.Agent
	a.Jobs = jobs.NewQueue(a.Store, a.Secret, a.Audit)
	a.Jobs.SetRunner(s.ExecuteJob)
	policies := map[string]jobs.SchedulePolicy{}
	for _, name := range []string{"reconcile"} {
		v, ok := s.Registry[name]
		if !ok {
			continue
		}
		policies[name] = jobs.SchedulePolicy{Effectful: v.Effectful, RequiresMFA: v.MFA, ObjectKey: func(raw json.RawMessage) (string, error) {
			args := v.NewArgs()
			d := json.NewDecoder(bytes.NewReader(raw))
			d.DisallowUnknownFields()
			if d.Decode(args) != nil {
				return "", Errf("bad_request", "Invalid scheduled arguments")
			}
			if validator, ok := args.(interface{ Validate() error }); ok {
				if e := validator.Validate(); e != nil {
					return "", e
				}
			}
			return v.ObjectKey(args), nil
		}}
	}
	a.Scheduler = jobs.NewScheduler(a.Jobs, nil, policies)
}

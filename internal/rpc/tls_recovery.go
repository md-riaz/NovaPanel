package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/store"
)

func (s *Server) prepareTLSRecovery(ctx context.Context) (*jobs.Job, error) {
	raw, err := s.Agent.Store.TLSIntent(ctx)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		for _, state := range []string{"running", "failed"} {
			before := ""
			for {
				rows, err := s.Agent.Store.ListJobs(ctx, store.JobFilter{State: state, Before: before, Limit: 200})
				if err != nil {
					return nil, err
				}
				for _, j := range rows {
					if j.Step == "tls_committed" && (j.Verb == "ssl.upload" || j.Verb == "ssl.issue" || j.Verb == "ssl.renew" || j.Verb == "site.update") && (j.State == "running" || j.Code == "interrupted") {
						composite, err := s.compositeTLSOwner(ctx, j)
						if err != nil {
							return nil, err
						}
						if composite {
							continue
						}
						if err = s.executeJob(ctx, &j, true); err != nil {
							return nil, err
						}
						return &j, nil
					}
				}
				if len(rows) < 200 {
					break
				}
				before = rows[len(rows)-1].ID
			}
		}
		return nil, nil
	}
	_, changed, err := s.Agent.Store.CheckCompositeTLSIntent(ctx, raw)
	if err != nil {
		return nil, err
	}
	if changed {
		return nil, Errf("agent_error", "Composite TLS recovery binding changed")
	}
	var binding struct {
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(raw, &binding) != nil || !id.Valid(binding.JobID) {
		return nil, Errf("precondition_failed", "Invalid TLS recovery journal")
	}
	j, err := s.Agent.Store.GetJob(ctx, binding.JobID)
	if err != nil {
		return nil, err
	}
	if j.Verb != "ssl.upload" && j.Verb != "ssl.issue" && j.Verb != "ssl.renew" && j.Verb != "site.update" {
		return nil, Errf("forbidden", "Invalid TLS recovery verb")
	}
	composite, err := s.compositeTLSOwner(ctx, j)
	if err != nil {
		return nil, err
	}
	if composite {
		if j.State != "queued" && j.State != "running" && !(j.State == "failed" && j.Code == "interrupted") {
			return nil, Errf("precondition_failed", "Invalid TLS recovery state")
		}
		return nil, nil
	}
	if j.State != "running" && !(j.State == "failed" && j.Code == "interrupted") {
		return nil, Errf("precondition_failed", "Invalid TLS recovery state")
	}
	if err = s.executeJob(ctx, &j, true); err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Server) compositeTLSOwner(ctx context.Context, j jobs.Job) (bool, error) {
	if j.Verb != "site.update" {
		return false, nil
	}
	op, err := s.Agent.Store.LoadOperation(ctx, j.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if op.Kind == store.SiteUpdate && (op.Checkpoint == "done" || op.Checkpoint == "expired") {
		return false, Errf("precondition_failed", "Invalid composite TLS recovery state")
	}
	return op.Kind == store.SiteUpdate, nil
}

func (s *Server) resumeTLSRecovery(ctx context.Context, j *jobs.Job) error {
	// Revalidate after acquiring running state. Never dispatch the external-effect Exec.
	if err := s.executeJob(ctx, j, true); err != nil {
		return err
	}
	err := s.Registry[j.Verb].Recover(ctx, s.Agent, j)
	pending, e := s.Agent.Store.TLSIntent(context.WithoutCancel(ctx))
	if e != nil || len(pending) > 0 {
		return Errf("interrupted", "TLS recovery unresolved")
	}
	return err
}

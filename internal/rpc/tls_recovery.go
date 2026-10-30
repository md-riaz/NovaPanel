package rpc

import (
	"context"
	"encoding/json"
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
					if j.Step == "tls_committed" && (j.Verb == "ssl.upload" || j.Verb == "ssl.issue" || j.Verb == "ssl.renew") && (j.State == "running" || j.Code == "interrupted") {
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
	if j.Verb != "ssl.upload" && j.Verb != "ssl.issue" && j.Verb != "ssl.renew" {
		return nil, Errf("forbidden", "Invalid TLS recovery verb")
	}
	if j.State != "running" && !(j.State == "failed" && j.Code == "interrupted") {
		return nil, Errf("precondition_failed", "Invalid TLS recovery state")
	}
	if err = s.executeJob(ctx, &j, true); err != nil {
		return nil, err
	}
	return &j, nil
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

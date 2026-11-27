package verbs

import (
	"context"
	"errors"

	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

// siteCompositeExec is kept behind admission until recovery is integrated.
func siteCompositeExec(ctx context.Context, a *agent.Agent, j *jobs.Job, v siteUpdateArgs) error {
	for {
		durable, err := a.Store.GetJob(ctx, j.ID)
		if err != nil {
			return err
		}
		children, err := a.Store.OperationChildren(ctx, j.ID)
		if err != nil {
			return err
		}
		next, err := jobs.NextCompositeChild(children)
		if err != nil {
			return err
		}
		if durable.Code == "agent_error" {
			next = len(children) - 1
			if children[next].Status != "pending" {
				return jobs.ErrRetry
			}
		}
		if next < 0 {
			if err = a.Store.CheckpointOperation(ctx, j.ID, "prepared", "verified"); err != nil {
				return err
			}
			for _, child := range children {
				if child.Status == "failed" && child.TypedError != "not_attempted" {
					return rpc.Errf(child.TypedError, "Composite update failed")
				}
			}
			return nil
		}
		child := children[next]
		if err = a.Jobs.CheckCompositeEffect(ctx, j.ID, v.SiteID); err != nil {
			return errors.Join(jobs.ErrRetry, err)
		}
		if child.Kind == "maintenance_off" {
			for _, pending := range children {
				if pending.Ordinal != child.Ordinal && pending.Status == "pending" {
					if err = a.Store.CommitOperationChild(ctx, j.ID, pending.Ordinal, pending.Checkpoint, "failed", "not_attempted"); err != nil {
						return errors.Join(jobs.ErrRetry, err)
					}
				}
			}
		}
		err = runCompositeEffect(ctx, a, j, v, child)
		if err != nil {
			if child.Kind == "maintenance_on" || child.Kind == "maintenance_off" || errors.Is(err, jobs.ErrRetry) || errors.Is(err, jobs.ErrRecoveryRequired) {
				return errors.Join(jobs.ErrRetry, err)
			}
			var coded *rpc.CodedError
			if !errors.As(err, &coded) {
				return errors.Join(jobs.ErrRetry, err)
			}
			if err = a.Store.CommitOperationChild(ctx, j.ID, child.Ordinal, child.Checkpoint, "failed", coded.Code); err != nil {
				return errors.Join(jobs.ErrRetry, err)
			}
			continue
		}
		if child.Kind == "php_change" {
			err = a.Store.CommitPHPChild(ctx, j.ID, child.Ordinal)
		} else {
			err = a.Store.CommitOperationChild(ctx, j.ID, child.Ordinal, "verified", "committed", "")
		}
		if err != nil {
			return errors.Join(jobs.ErrRetry, err)
		}
	}
}

func runCompositeEffect(ctx context.Context, a *agent.Agent, j *jobs.Job, v siteUpdateArgs, child store.OperationChild) error {
	switch child.Kind {
	case "maintenance_on", "maintenance_off":
		value := true
		if child.Kind == "maintenance_off" {
			ctx = agent.MaintenanceFinalizer(ctx, j.ID)
		}
		if child.Kind == "maintenance_on" {
			if err := a.Store.EngageOperationBracket(ctx, j.ID); err != nil {
				return err
			}
		} else if v.Maintenance != nil {
			value = *v.Maintenance
		} else if v.PHPVersion != nil && (v.PrimaryDomainID != nil || v.ForceHTTPS != nil || v.HSTSMaxAge != nil) {
			value = false
		} else {
			site, err := a.Store.SiteByID(ctx, v.SiteID)
			if err != nil {
				return err
			}
			value = site.Maintenance
		}
		if child.Kind == "maintenance_off" {
			return (siteMutationState{a}).applyMaintenanceFinalizer(ctx, v.SiteID, j.ID, value, j)
		}
		return (siteMutationState{a}).applySitePatch(ctx, v.SiteID, store.SitePatch{Maintenance: &value}, j)
	case "php_change":
		if child.Identity.PHP == nil || !(phpSetRuntime{a}).available() {
			return rpc.Errf("unsupported", "PHP execution unavailable")
		}
		site, err := a.Store.SiteByID(ctx, v.SiteID)
		if err != nil {
			return err
		}
		old, desired, err := phpEffectStates(ctx, a, site, *child.Identity.PHP)
		if err != nil {
			return err
		}
		if err = phpPublishEffect(ctx, a, site.Name, desired, old); err != nil {
			return err
		}
		if child.Checkpoint == "prepared" {
			if err = a.Store.CheckpointOperationChild(ctx, j.ID, child.Ordinal, "prepared", "config_published"); err != nil {
				return err
			}
			child.Checkpoint = "config_published"
		}
		if child.Checkpoint == "config_published" {
			return a.Store.CheckpointOperationChild(ctx, j.ID, child.Ordinal, "config_published", "verified")
		}
		return nil
	case "domain_tls":
		v.PHPVersion, v.Maintenance = nil, nil
		return siteUpdateEffect(ctx, a, j, v)
	}
	return rpc.Errf("invalid_argument", "Invalid composite child")
}

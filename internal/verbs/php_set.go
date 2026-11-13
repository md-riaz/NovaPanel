package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"time"
)

type phpSetArgs struct {
	SiteID  string          `json:"site_id"`
	Version string          `json:"version"`
	Limits  store.PHPLimits `json:"limits,omitempty"`
}

func (v *phpSetArgs) Validate() error {
	if err := (&siteIDArgs{v.SiteID}).Validate(); err != nil {
		return err
	}
	if v.Version == "" {
		return rpc.Errf("invalid_argument", "PHP version is required")
	}
	if v.Version != "8.3" {
		return rpc.Errf("unsupported", "Unsupported PHP version")
	}
	return php.ValidateLimits(v.Limits)
}
func init() {
	rpc.Register(rpc.Verb{Name: "php.set", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &phpSetArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*phpSetArgs).SiteID }, Run: phpSet, Exec: phpSetExec})
}
func phpAvailable(ctx context.Context, runner exec.Runner) error {
	if runner == nil {
		return rpc.Errf("agent_error", "PHP runtime unavailable")
	}
	available, err := phpRuntimeAvailable(ctx, runner)
	if err != nil {
		return err
	}
	if !available {
		return rpc.Errf("precondition_failed", "PHP runtime is not installed")
	}
	return nil
}
func committedPHPCap(limits store.PHPLimits) (int, error) {
	if limits.MaxChildren == nil || *limits.MaxChildren < 2 || *limits.MaxChildren > 32 {
		return 0, rpc.Errf("precondition_failed", "PHP concurrency cap is missing or invalid")
	}
	return *limits.MaxChildren, nil
}

// activePHPLimits commits the one-time legacy cap before any pool recovery.
func activePHPLimits(ctx context.Context, st *store.Store, siteID string) (store.PHPLimits, error) {
	return st.BackfillSitePHPCap(ctx, siteID, func(active int) int {
		return php.ChildrenCap(php.TotalMemoryMB(), active)
	})
}
func phpSet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*phpSetArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	s, err := a.Store.SiteByID(ctx, v.SiteID)
	if err != nil {
		return nil, siteError(err)
	}
	if s.Type == "static" || s.Status != "active" {
		return nil, rpc.Errf("precondition_failed", "Site must be an active PHP site")
	}
	if err = phpAvailable(ctx, a.Exec); err != nil {
		return nil, err
	}
	return a.Jobs.Enqueue(ctx, "php.set", v, "site:"+v.SiteID)
}
func phpSetExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	var v phpSetArgs
	if err := json.Unmarshal(j.Args, &v); err != nil {
		return rpc.Errf("bad_request", "Invalid PHP arguments")
	}
	if err := v.Validate(); err != nil {
		return err
	}
	s, err := a.Store.SiteByID(ctx, v.SiteID)
	if err != nil {
		return siteError(err)
	}
	if s.Type == "static" || s.Status != "active" {
		return rpc.Errf("precondition_failed", "Site must be an active PHP site")
	}
	if a.Jobs == nil || a.PHP == nil || a.Exec == nil {
		return rpc.Errf("agent_error", "PHP execution unavailable")
	}
	unlock, err := a.Jobs.LockSiteOSUser(ctx, s.ID, s.Name)
	if err != nil {
		return err
	}
	defer unlock()
	s, err = a.Store.SiteByID(ctx, v.SiteID)
	if err != nil {
		return siteError(err)
	}
	if s.Type == "static" || s.Status != "active" {
		return rpc.Errf("precondition_failed", "Site must be an active PHP site")
	}
	if err = php.VerifyUser(ctx, a.Exec, a.Cfg.SitesRoot, s.Name, s.ID); err != nil {
		return err
	}
	if err = phpAvailable(ctx, a.Exec); err != nil {
		return err
	}
	limits, err := activePHPLimits(ctx, a.Store, s.ID)
	if err != nil {
		return err
	}
	cap, err := committedPHPCap(limits)
	if err != nil {
		return err
	}
	committed := php.PoolState{SiteID: s.ID, Exists: true, MaxChildren: cap, Limits: limits}
	if j.Attempts > 1 {
		if err = a.PHP.Recover(ctx, s.Name, committed); err != nil {
			return err
		}
	}
	if v.Limits.MemoryMB != nil {
		limits.MemoryMB = v.Limits.MemoryMB
	}
	if v.Limits.UploadMB != nil {
		limits.UploadMB = v.Limits.UploadMB
	}
	if v.Limits.MaxExecSec != nil {
		limits.MaxExecSec = v.Limits.MaxExecSec
	}
	if err = a.PHP.ApplyCommitted(ctx, s.Name, php.PoolState{SiteID: s.ID, Exists: true, MaxChildren: cap, Limits: limits}, committed); err != nil {
		return err
	}
	if err = a.Store.SetSitePHP(ctx, s.ID, v.Version, limits); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		current, readErr := a.Store.SitePHPLimits(cleanup, s.ID)
		if readErr != nil {
			return errors.Join(err, readErr, jobs.ErrRetry)
		}
		currentCap, capErr := committedPHPCap(current)
		if capErr != nil {
			return errors.Join(err, capErr, jobs.ErrRetry)
		}
		committed.Limits = current
		committed.MaxChildren = currentCap
		return errors.Join(err, a.PHP.Recover(cleanup, s.Name, committed), jobs.ErrRetry)
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

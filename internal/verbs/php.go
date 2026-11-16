package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	mainphp "novapanel/internal/php"
	"novapanel/internal/render/nginx"
	php "novapanel/internal/render/php"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"time"
)

func init() {
	rpc.Register(rpc.Verb{Name: "php.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "sites:read", NewArgs: func() any { return &emptyArgs{} }, Run: phpList})
	rpc.Register(rpc.Verb{Name: "php.set", MinRole: rpc.Admin, Scope: "sites:write", Slow: true, NewArgs: func() any { return &phpSetArgs{} }, ObjectKey: func(v any) string { return "site:" + v.(*phpSetArgs).request.SiteID }, Run: phpSet, Exec: phpSetExec})
}

type phpSetArgs struct{ request php.SetRequest }

func (v *phpSetArgs) UnmarshalJSON(raw []byte) error {
	request, err := php.DecodeSetRequest(raw)
	if err != nil {
		return err
	}
	v.request = request
	return nil
}

func phpSet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	if !(phpSetRuntime{a}).available() {
		return nil, rpc.Errf("unsupported", "PHP execution unavailable")
	}
	request := args.(*phpSetArgs).request
	return a.Jobs.EnqueueOperation(ctx, store.OperationRequest{Kind: store.PHPSet, SiteID: request.SiteID, PHP: &request}, nil)
}

// No default dependencies are installed until startup recovery owns snapshots.
type phpSetRuntime struct{ agent *agent.Agent }

func (r phpSetRuntime) available() bool {
	a := r.agent
	return a != nil && a.Store != nil && a.Jobs != nil && a.PHP != nil && a.PHP.Runner != nil && a.PHP.Probe != nil && a.Exec != nil
}

func phpSetExec(ctx context.Context, a *agent.Agent, j *jobs.Job) error {
	if j == nil || !(phpSetRuntime{a}).available() {
		return rpc.Errf("unsupported", "PHP execution unavailable")
	}
	op, err := a.Store.LoadOperation(ctx, j.ID)
	if err != nil {
		return err
	}
	if op.Kind != store.PHPSet || op.Identity.Site == nil || op.Identity.PHP == nil || j.ObjectKey != "site:"+op.SiteID {
		return rpc.Errf("precondition_failed", "Invalid PHP operation")
	}
	return phpSetEffect(ctx, a, j, op)
}

func phpSetEffect(ctx context.Context, a *agent.Agent, j *jobs.Job, op store.Operation) error {
	var err error
	check := func(c context.Context) error { return a.Jobs.CheckPHPEffect(c, j.ID, op.SiteID) }
	if err := check(ctx); err != nil {
		return err
	}
	site, p := op.Identity.Site, op.Identity.PHP
	if err = mainphp.VerifyUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID); err != nil {
		return err
	}
	if err = phpAvailable(ctx, a.Exec); err != nil {
		return err
	}
	toLimits := func(v php.Limits, cap int) store.PHPLimits {
		return store.PHPLimits{MemoryMB: &v.MemoryMB, UploadMB: &v.UploadMB, MaxExecSec: &v.MaxExecSec, MaxChildren: &cap}
	}
	old := mainphp.PoolState{SiteID: site.ID, Exists: true, MaxChildren: p.OriginalCap, Limits: toLimits(p.OriginalLimits, p.OriginalCap)}
	desired := mainphp.PoolState{SiteID: site.ID, Exists: true, MaxChildren: p.DesiredCap, Limits: toLimits(p.DesiredLimits, p.DesiredCap)}
	restore := func() error {
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := a.PHP.Recover(recovery, site.Name, old); err != nil {
			return errors.Join(nginx.ErrPHPRecoveryRequired, err)
		}
		return nginx.ErrPHPOriginalRestored
	}
	if a.Jobs.PHPRecoveryOnly(ctx, j.ID) || j.Attempts > 1 {
		return restore()
	}
	if op.Checkpoint != "prepared" {
		return restore()
	}
	if err = a.PHP.ApplyCommitted(ctx, site.Name, desired, old); err != nil {
		if errors.Is(err, jobs.ErrRetry) {
			return errors.Join(nginx.ErrPHPRecoveryRequired, err)
		}
		return err
	}
	if err = check(ctx); err != nil {
		return restore()
	}
	if err = a.Store.CheckpointOperation(ctx, j.ID, "prepared", "config_published"); err != nil {
		return restore()
	}
	if err = check(ctx); err != nil {
		return restore()
	}
	if err = a.Store.CheckpointOperation(ctx, j.ID, "config_published", "verified"); err != nil {
		return restore()
	}
	j.Result = json.RawMessage(`{}`)
	return nil
}

var _ json.Unmarshaler = (*phpSetArgs)(nil)

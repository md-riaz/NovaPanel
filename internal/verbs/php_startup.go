package verbs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"novapanel/internal/exec"
	"novapanel/internal/jobs"
	"novapanel/internal/php"
	"novapanel/internal/store"
)

// RecoverCommittedPHP restores active pool projections under the lifecycle locks.
// It must complete before workers or the RPC listener can start.
func RecoverCommittedPHP(ctx context.Context, st *store.Store, q *jobs.Queue, runner exec.Runner, pool *php.PoolApplier, sitesRoot string) error {
	if st == nil || q == nil || runner == nil || pool == nil {
		return errors.New("PHP recovery dependencies unavailable")
	}
	sites, err := st.ListSites(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range sites {
		unlock, err := q.LockSiteOSUser(ctx, candidate.ID, candidate.Name)
		if err != nil {
			return err
		}
		err = func() error {
			s, err := st.SiteByID(ctx, candidate.ID)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if s.Name != candidate.Name || s.Status != "active" || s.Type == "static" {
				return nil
			}
			if err = php.VerifyUser(ctx, runner, sitesRoot, s.Name, s.ID); err != nil {
				return err
			}
			limits, err := activePHPLimits(ctx, st, s.ID)
			if err != nil {
				return err
			}
			cap, err := committedPHPCap(limits)
			if err != nil {
				return err
			}
			return pool.Recover(ctx, s.Name, php.PoolState{SiteID: s.ID, Exists: true, MaxChildren: cap, Limits: limits})
		}()
		unlock()
		if err != nil {
			return fmt.Errorf("recover PHP pool for %s: %w", candidate.Name, err)
		}
	}
	return nil
}

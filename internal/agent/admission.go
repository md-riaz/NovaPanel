package agent

import (
	"context"
	"novapanel/internal/rpcerr"
)

// PrepareStartup is synchronous; recovery audit and effects do not use RPC admission.
func (a *Agent) PrepareStartup(ctx context.Context) error {
	a.CloseMutationAdmission()
	if err := a.Jobs.PrepareStartup(ctx); err != nil {
		return err
	}
	a.OpenMutationAdmission()
	return nil
}

// CloseMutationAdmission is startup-only wiring, before listeners or schedulers.
func (a *Agent) CloseMutationAdmission() { a.mutationsBlocked.Store(true) }
func (a *Agent) OpenMutationAdmission()  { a.mutationsBlocked.Store(false) }
func (a *Agent) CheckMutationAdmission() error {
	if a.mutationsBlocked.Load() {
		return rpcerr.Errf("precondition_failed", "Startup recovery unresolved")
	}
	return nil
}

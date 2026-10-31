package nginx

import (
	"context"
	"errors"
	"time"

	"novapanel/internal/platform"
	"novapanel/internal/render/php"
	"novapanel/internal/rpcerr"
)

// ApplyPHP coordinates in-process publication only. Caller holds ordered site
// locks and must supply atomic durable metadata publication before wiring workers.
// restoredProbe checks the prior service state, including first-create absence.
func (a *Applier) ApplyPHP(ctx context.Context, pools *php.Applier, d Desired, commit func(context.Context) error, restoredProbe func(context.Context) error) error {
	if pools == nil || d.PHP == nil || commit == nil || restoredProbe == nil || a.Runner == nil || a.Probe == nil {
		return rpcerr.Errf("agent_error", "PHP transaction dependencies unavailable")
	}
	b, err := Render(d)
	if err != nil {
		return err
	}
	return a.Transaction(ctx, func(n *Transaction) error {
		if a.Guard != nil {
			if err := a.Guard(ctx); err != nil {
				return err
			}
		}
		old, err := n.Snapshot(d.Name)
		if err != nil {
			return err
		}
		// Live bytes are not proof of health after an interrupted publication.
		old.Config = old.LKG
		if old.LKG == nil {
			old.Enabled = false
		}
		return pools.Transaction(ctx, *d.PHP, func(p *php.Transaction) (result error) {
			reloadAttempted := false
			defer func() {
				if result == nil {
					return
				}
				recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				restoreErr := errors.Join(p.Restore(), n.Restore(recovery, old, false))
				result = errors.Join(result, restoreErr)
				if restoreErr != nil {
					return
				}
				if !reloadAttempted {
					return
				}
				// Restore both files, validate both, restore FPM before nginx, then probe.
				if err := p.Validate(recovery); err != nil {
					result = errors.Join(result, err)
					return
				}
				if err := a.command(recovery, platform.Nginx, []string{"-t"}); err != nil {
					result = errors.Join(result, err)
					return
				}
				if err := p.Reload(recovery); err != nil {
					result = errors.Join(result, err)
					return
				}
				if err := a.command(recovery, platform.Systemctl, []string{"reload", "nginx"}); err != nil {
					result = errors.Join(result, err)
					return
				}
				result = errors.Join(result, restoredProbe(recovery))
			}()
			if err := p.Stage(); err != nil {
				return err
			}
			staged := Snapshot{Name: d.Name, Config: b, LKG: old.LKG, Enabled: true}
			if err := n.Restore(ctx, staged, false); err != nil {
				return err
			}
			if err := p.Validate(ctx); err != nil {
				return err
			}
			if err := a.command(ctx, platform.Nginx, []string{"-t"}); err != nil {
				return err
			}
			reloadAttempted = true
			if err := p.Reload(ctx); err != nil {
				return err
			}
			if err := pools.Probe(ctx, *d.PHP); err != nil {
				return err
			}
			if err := a.command(ctx, platform.Systemctl, []string{"reload", "nginx"}); err != nil {
				return err
			}
			if err := a.Probe(ctx, d); err != nil {
				return err
			}
			if err := p.Promote(); err != nil {
				return err
			}
			staged.LKG = b
			if err := n.Restore(ctx, staged, false); err != nil {
				return err
			}
			return commit(ctx)
		})
	})
}

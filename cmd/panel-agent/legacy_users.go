package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"novapanel/internal/agent"
	"novapanel/internal/php"
)

const legacyUsersSetting = "migration:site-user-markers:v1"

// migrateLegacyUsers runs before the listener and worker, while the database's
// lifetime lock excludes another agent. Completion is recorded only after every
// row was examined, so interruption safely resumes the same bounded pass.
func migrateLegacyUsers(parent context.Context, a *agent.Agent) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	value, err := a.Store.Setting(ctx, legacyUsersSetting)
	if err == nil {
		if string(value) != "true" {
			return errors.New("invalid site user migration state")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	sites, err := a.Store.ListSites(ctx)
	if err != nil {
		return err
	}
	for _, site := range sites {
		unlock, err := a.Jobs.LockSiteOSUser(ctx, site.ID, site.Name)
		if err != nil {
			return err
		}
		current, err := a.Store.SiteByID(ctx, site.ID)
		if err == nil && current.Name == site.Name {
			// A creating or failed row can precede a collision check and therefore
			// does not prove that an unmarked account belongs to the panel.
			if current.Status == "active" {
				err = php.MigrateUser(ctx, a.Exec, a.Cfg.SitesRoot, site.Name, site.ID)
			}
		} else if err == nil {
			err = errors.New("site identity changed during migration")
		}
		unlock()
		if err != nil {
			return fmt.Errorf("migrate site user %s: %w", site.Name, err)
		}
	}
	return a.Store.SetSetting(ctx, legacyUsersSetting, []byte("true"))
}

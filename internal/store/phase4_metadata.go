package store

import (
	"context"
	"database/sql"
	"errors"
	php "novapanel/internal/render/php"
	"regexp"
	"strings"
)

// GetPHPLimits reads desired metadata, never a pool file.
func (s *Store) GetPHPLimits(ctx context.Context, id string) (v php.Limits, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT memory_mb,upload_mb,max_exec_sec FROM site_php_limits WHERE site_id=?", id).Scan(&v.MemoryMB, &v.UploadMB, &v.MaxExecSec)
	return
}

// MergePHPLimits is metadata-only. A runtime caller must hold the site lock and
// publish/probe the pool before committing its verified desired tuple here.
func (s *Store) MergePHPLimits(ctx context.Context, id string, p php.LimitsPatch) (v php.Limits, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		var kind string
		if e := tx.QueryRowContext(ctx, "SELECT type FROM sites WHERE id=?", id).Scan(&kind); e != nil {
			return e
		}
		if kind != "php" && kind != "wordpress" {
			return ErrSitePrecondition
		}
		var current php.Limits
		e := tx.QueryRowContext(ctx, "SELECT memory_mb,upload_mb,max_exec_sec FROM site_php_limits WHERE site_id=?", id).Scan(&current.MemoryMB, &current.UploadMB, &current.MaxExecSec)
		var before *php.Limits
		if e == nil {
			before = &current
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		v, e = php.MergeLimits(before, p)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO site_php_limits(site_id,memory_mb,upload_mb,max_exec_sec) VALUES(?,?,?,?) ON CONFLICT(site_id) DO UPDATE SET memory_mb=excluded.memory_mb,upload_mb=excluded.upload_mb,max_exec_sec=excluded.max_exec_sec", id, v.MemoryMB, v.UploadMB, v.MaxExecSec)
		return e
	})
	return
}

type DatabasePrimary struct{ ID, UserID, SiteID string }
type Database struct {
	ID, SiteID, Name, Engine, PrimaryUserID string
	Created                                 int64
}

var phase4ResourceID = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// CreateDatabasePrimary allocates only metadata, not MariaDB resources.
// Workers must use the atomic journal terminal API once available, not this
// helper followed by separate job/claim writes.
func (s *Store) CreateDatabasePrimary(ctx context.Context, p DatabasePrimary) error {
	if !phase4ResourceID.MatchString(p.ID) || !phase4ResourceID.MatchString(p.UserID) {
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		now := s.clock.Now()
		var site any
		if p.SiteID != "" {
			site = p.SiteID
		}
		if _, e := tx.ExecContext(ctx, "INSERT INTO databases(id,site_id,name,primary_user_id,created) VALUES(?,?,?,?,?)", p.ID, site, "d_"+strings.ToLower(p.ID), p.UserID, now); e != nil {
			return siteWriteError(e)
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO db_users(id,db_id,username,host,created) VALUES(?,?,?,'127.0.0.1',?)", p.UserID, p.ID, "u_"+strings.ToLower(p.UserID), now)
		return siteWriteError(e)
	})
}
func (s *Store) GetDatabase(ctx context.Context, id string) (v Database, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT id,coalesce(site_id,''),name,engine,primary_user_id,created FROM databases WHERE id=?", id).Scan(&v.ID, &v.SiteID, &v.Name, &v.Engine, &v.PrimaryUserID, &v.Created)
	return
}

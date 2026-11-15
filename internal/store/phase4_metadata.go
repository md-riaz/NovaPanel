package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	php "novapanel/internal/render/php"
	"regexp"
	"strings"
)

// WordPressDatabaseID reads the persisted site/database binding.
func (s *Store) WordPressDatabaseID(ctx context.Context, siteID string) (id string, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT db_id FROM wordpress_bindings WHERE site_id=?", siteID).Scan(&id)
	return
}

// readPhase4PHPLimits reads the same settings record used by the PHP runtime.
func readPhase4PHPLimits(ctx context.Context, tx *sql.Tx, id string) (*php.Limits, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", phpLimitsKey(id)).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var saved storedPHPLimits
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		return nil, err
	}
	limits, err := php.MergeLimits(nil, php.LimitsPatch{MemoryMB: saved.MemoryMB, UploadMB: saved.UploadMB, MaxExecSec: saved.MaxExecSec})
	if err != nil {
		return nil, err
	}
	return &limits, nil
}
func writePhase4PHPLimits(ctx context.Context, tx *sql.Tx, id string, limits php.Limits) error {
	var raw string
	var saved storedPHPLimits
	err := tx.QueryRowContext(ctx, "SELECT value_json FROM settings WHERE key=?", phpLimitsKey(id)).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal([]byte(raw), &saved); err != nil {
			return err
		}
	}
	saved.MemoryMB, saved.UploadMB, saved.MaxExecSec = &limits.MemoryMB, &limits.UploadMB, &limits.MaxExecSec
	encoded, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json", phpLimitsKey(id), string(encoded))
	return err
}
func (s *Store) GetPHPLimits(ctx context.Context, id string) (v php.Limits, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		limits, e := readPhase4PHPLimits(ctx, tx, id)
		if e != nil {
			return e
		}
		v, e = php.MergeLimits(limits, php.LimitsPatch{})
		return e
	})
	return
}
func (s *Store) MergePHPLimits(ctx context.Context, id string, patch php.LimitsPatch) (v php.Limits, err error) {
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		var typ string
		if e := tx.QueryRowContext(ctx, "SELECT type FROM sites WHERE id=?", id).Scan(&typ); e != nil {
			return e
		}
		if typ != "php" && typ != "wordpress" {
			return ErrSitePrecondition
		}
		old, e := readPhase4PHPLimits(ctx, tx, id)
		if e != nil {
			return e
		}
		v, e = php.MergeLimits(old, patch)
		if e != nil {
			return e
		}
		return writePhase4PHPLimits(ctx, tx, id, v)
	})
	return
}

type DatabasePrimary struct{ ID, UserID, SiteID string }
type OperationDatabaseRecord struct {
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
		if _, e := tx.ExecContext(ctx, "INSERT INTO databases(id,site_id,name,created) VALUES(?,?,?,?)", p.ID, site, "d_"+strings.ToLower(p.ID), now); e != nil {
			return siteWriteError(e)
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO db_users(id,db_id,username,host,owner,created) VALUES(?,?,?,'127.0.0.1',1,?)", p.UserID, p.ID, "u_"+strings.ToLower(p.UserID), now)
		return siteWriteError(e)
	})
}

// ListDatabases returns persisted database metadata without credentials.
func (s *Store) ListOperationDatabases(ctx context.Context) ([]OperationDatabaseRecord, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,coalesce(site_id,''),name,engine,(SELECT id FROM db_users WHERE db_id=databases.id AND owner=1),created FROM databases ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationDatabaseRecord{}
	for rows.Next() {
		var v OperationDatabaseRecord
		if err := rows.Scan(&v.ID, &v.SiteID, &v.Name, &v.Engine, &v.PrimaryUserID, &v.Created); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetDatabase(ctx context.Context, id string) (v OperationDatabaseRecord, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT id,coalesce(site_id,''),name,engine,(SELECT id FROM db_users WHERE db_id=databases.id AND owner=1),created FROM databases WHERE id=?", id).Scan(&v.ID, &v.SiteID, &v.Name, &v.Engine, &v.PrimaryUserID, &v.Created)
	return
}

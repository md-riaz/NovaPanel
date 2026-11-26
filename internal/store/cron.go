package store

import (
	"context"
	"database/sql"
)

type CronJob struct {
	ID, SiteID, Schedule, ArgvJSON, UnitName string
	Enabled                                  bool
	Created                                  int64
}

func (s *Store) InsertCronTx(ctx context.Context, tx *sql.Tx, v CronJob) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO cron_jobs(id,site_id,schedule,argv_json,unit_name,enabled,created) VALUES(?,?,?,?,?,0,?)", v.ID, v.SiteID, v.Schedule, v.ArgvJSON, v.UnitName, v.Created)
	return err
}

func (s *Store) SetCronEnabled(ctx context.Context, id string, enabled bool) error {
	r, err := s.db.ExecContext(ctx, "UPDATE cron_jobs SET enabled=? WHERE id=?", enabled, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) CronByID(ctx context.Context, id string) (v CronJob, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT id,site_id,schedule,argv_json,unit_name,enabled,created FROM cron_jobs WHERE id=?", id).Scan(&v.ID, &v.SiteID, &v.Schedule, &v.ArgvJSON, &v.UnitName, &v.Enabled, &v.Created)
	return
}

func (s *Store) ListCronBySite(ctx context.Context, siteID string, limit int) ([]CronJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,site_id,schedule,argv_json,unit_name,enabled,created FROM cron_jobs WHERE site_id=? ORDER BY created DESC,id DESC LIMIT ?", siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CronJob{}
	for rows.Next() {
		var v CronJob
		if err := rows.Scan(&v.ID, &v.SiteID, &v.Schedule, &v.ArgvJSON, &v.UnitName, &v.Enabled, &v.Created); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCron(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM cron_jobs WHERE id=?", id)
	return err
}

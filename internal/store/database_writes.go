package store

import (
	"context"
	"database/sql"
	"errors"
)

type DatabaseUser struct {
	ID, DatabaseID, Username string
	Owner                    bool
}

func (s *Store) ReserveDatabaseUser(ctx context.Context, u DatabaseUser) error {
	if u.ID == "" || u.DatabaseID == "" || u.Username == "" {
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var db, name, host string
		err := tx.QueryRowContext(ctx, `SELECT db_id,username,host FROM db_users WHERE id=?`, u.ID).Scan(&db, &name, &host)
		if err == nil {
			if db != u.DatabaseID || name != u.Username || host != "127.0.0.1" {
				return ErrSiteConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO db_users(id,db_id,username,created) VALUES(?,?,?,?)`, u.ID, u.DatabaseID, u.Username, s.clock.Now())
		return siteWriteError(err)
	})
}

// CheckDatabaseReservation verifies the desired row exists without creating it.
func (s *Store) CheckDatabaseReservation(ctx context.Context, d Database, u DatabaseUser) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM databases d JOIN db_users u ON u.db_id=d.id WHERE d.id=? AND d.name=? AND d.engine=? AND d.site_id IS ? AND u.id=? AND u.username=? AND u.host='127.0.0.1'`, d.ID, d.Name, d.Engine, d.SiteID, u.ID, u.Username).Scan(&n)
	if err != nil {
		return err
	}
	if u.DatabaseID != d.ID || n != 1 {
		return ErrSiteConflict
	}
	return nil
}

// ReserveDatabase persists a database and its initial account together.
// A repeat is accepted only if both stored identities match exactly.
func (s *Store) ReserveDatabase(ctx context.Context, d Database, u DatabaseUser) error {
	if d.ID == "" || d.Name == "" || d.Engine != "mariadb" || u.ID == "" || u.DatabaseID != d.ID || u.Username == "" {
		return ErrSiteInvalid
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var name, engine string
		var site sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT name,site_id,engine FROM databases WHERE id=?`, d.ID).Scan(&name, &site, &engine)
		if err == nil {
			if name != d.Name || engine != d.Engine || site.Valid != (d.SiteID != nil) || (site.Valid && site.String != *d.SiteID) {
				return ErrSiteConflict
			}
			var dbID, username, host string
			if err = tx.QueryRowContext(ctx, `SELECT db_id,username,host FROM db_users WHERE id=?`, u.ID).Scan(&dbID, &username, &host); err != nil {
				return err
			}
			if dbID != d.ID || username != u.Username || host != "127.0.0.1" {
				return ErrSiteConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO databases(id,site_id,name,engine,created) VALUES(?,?,?,?,?)`, d.ID, d.SiteID, d.Name, d.Engine, s.clock.Now()); err != nil {
			return siteWriteError(err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO db_users(id,db_id,username,owner,created) VALUES(?,?,?,1,?)`, u.ID, u.DatabaseID, u.Username, s.clock.Now())
		return siteWriteError(err)
	})
}

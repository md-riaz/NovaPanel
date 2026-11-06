package store

import (
	"context"
	"database/sql"
)

type Database struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	SiteID *string `json:"site_id"`
	Engine string  `json:"engine"`
}

func (s *Store) DatabaseWithUsers(ctx context.Context, id string) (Database, []DatabaseUser, error) {
	var d Database
	var site sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT id,name,site_id,engine FROM databases WHERE id=?`, id).Scan(&d.ID, &d.Name, &site, &d.Engine); err != nil {
		return d, nil, err
	}
	if site.Valid {
		d.SiteID = &site.String
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,db_id,username,owner FROM db_users WHERE db_id=? AND host='127.0.0.1' ORDER BY created,id`, id)
	if err != nil {
		return d, nil, err
	}
	defer rows.Close()
	var users []DatabaseUser
	for rows.Next() {
		var u DatabaseUser
		if err = rows.Scan(&u.ID, &u.DatabaseID, &u.Username, &u.Owner); err != nil {
			return d, nil, err
		}
		users = append(users, u)
	}
	return d, users, rows.Err()
}

func (s *Store) DatabaseUserByID(ctx context.Context, id string) (DatabaseUser, error) {
	var u DatabaseUser
	err := s.db.QueryRowContext(ctx, `SELECT id,db_id,username,owner FROM db_users WHERE id=? AND host='127.0.0.1'`, id).Scan(&u.ID, &u.DatabaseID, &u.Username, &u.Owner)
	return u, err
}
func (s *Store) DeleteDatabaseUser(ctx context.Context, u DatabaseUser) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM db_users WHERE id=? AND db_id=? AND username=? AND host='127.0.0.1'`, u.ID, u.DatabaseID, u.Username)
	return err
}

func (s *Store) DeleteDatabase(ctx context.Context, id, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM databases WHERE id=? AND name=?`, id, name)
	return err
}

func (s *Store) DeleteDatabaseForJob(ctx context.Context, id, name, jobID string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		// Preserve cascade identities in the existing job authority before the FK
		// cascade removes them; the snapshot and deletion must commit together.
		r, err := tx.ExecContext(ctx, `UPDATE jobs SET args_json=json_set(args_json,'$.users',json((SELECT json_group_array(json_object('id',id,'username',username)) FROM db_users WHERE db_id=? AND host='127.0.0.1'))) WHERE id=? AND verb='database.delete' AND state='running' AND object_key=?`, id, jobID, "db:"+id)
		if err = affected(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM databases WHERE id=? AND name=?`, id, name)
		return err
	})
}

func (s *Store) ListDatabases(ctx context.Context) ([]Database, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, site_id, engine FROM databases ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		var site sql.NullString
		if err := rows.Scan(&d.ID, &d.Name, &site, &d.Engine); err != nil {
			return nil, err
		}
		if site.Valid {
			d.SiteID = &site.String
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

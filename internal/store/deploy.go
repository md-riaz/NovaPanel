package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"novapanel/internal/id"
)

type DeployConfig struct {
	SiteID, RepoURL, Branch, PublicKey string
	Created, Updated                   int64
}

type Deploy struct {
	SHA     string `json:"sha"`
	State   string `json:"state"`
	Created int64  `json:"created"`
}

func validDeployName(name string) bool {
	return strings.HasPrefix(name, "deploy:") && id.Valid(strings.TrimPrefix(name, "deploy:"))
}

func (s *Store) DeployConfig(ctx context.Context, siteID string) (v DeployConfig, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT site_id,repo_url,branch,public_key,created,updated FROM deploy_config WHERE site_id=?", siteID).Scan(&v.SiteID, &v.RepoURL, &v.Branch, &v.PublicKey, &v.Created, &v.Updated)
	return
}

// CreateDeployConfig commits the key and public configuration together.
func (s *Store) CreateDeployConfig(ctx context.Context, v DeployConfig, sealed ACMESealed) error {
	name := "deploy:" + v.SiteID
	if !validDeployName(name) || !validACMESealed(sealed) {
		return errors.New("invalid deploy key")
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?) ON CONFLICT(name) DO NOTHING", name, name, sealed.Nonce, sealed.Ciphertext, s.clock.Now())
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("deploy key already exists")
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO deploy_config(site_id,repo_url,branch,public_key,created,updated) VALUES(?,?,?,?,?,?)", v.SiteID, v.RepoURL, v.Branch, v.PublicKey, v.Created, v.Updated)
		return err
	})
}

func (s *Store) UpdateDeployConfig(ctx context.Context, siteID, repoURL, branch string, updated int64) error {
	r, err := s.db.ExecContext(ctx, "UPDATE deploy_config SET repo_url=?,branch=?,updated=? WHERE site_id=?", repoURL, branch, updated, siteID)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) DeployKey(ctx context.Context, siteID string) (v ACMESealed, err error) {
	name := "deploy:" + siteID
	if !validDeployName(name) {
		return v, errors.New("invalid deploy key name")
	}
	err = s.db.QueryRowContext(ctx, "SELECT nonce,ciphertext FROM secrets WHERE name=?", name).Scan(&v.Nonce, &v.Ciphertext)
	if err == nil && !validACMESealed(v) {
		return ACMESealed{}, errors.New("invalid sealed deploy key")
	}
	return
}

func (s *Store) DeleteDeployKey(ctx context.Context, siteID string) error {
	name := "deploy:" + siteID
	if !validDeployName(name) {
		return errors.New("invalid deploy key name")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE name=?", name)
	return err
}

func (s *Store) DeployHistory(ctx context.Context, siteID string, limit int, successful bool) ([]Deploy, error) {
	query := "SELECT COALESCE(sha,''),state,created FROM deploys WHERE site_id=?"
	if successful {
		query += " AND state='done'"
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY created DESC, id DESC LIMIT ?", siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Deploy{}
	for rows.Next() {
		var v Deploy
		if err := rows.Scan(&v.SHA, &v.State, &v.Created); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

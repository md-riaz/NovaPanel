package store

import (
	"context"
	"database/sql"
	"errors"

	"novapanel/internal/backup"
	"novapanel/internal/secret"
)

var ErrRepositoryConflict = errors.New("repository conflict")
var ErrRepositoryMetadata = errors.New("repository metadata unavailable")

// CreatePanelSnapshot admits one store-lifetime operation for the entire private
// staging operation. VACUUM runs on the existing pool outside a transaction.
func (s *Store) CreatePanelSnapshot(ctx context.Context, scratch string) (*backup.Snapshot, error) {
	ctx, end, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer end()
	return backup.CreateSnapshot(ctx, s.db.raw, scratch)
}

// CreateRepository atomically publishes metadata and an already sealed password.
// The caller must initialize restic and arrange the F19 claim; this is not an RPC
// implementation. No migration is installed here: Phase 6 numbering is unassigned.
func (s *Store) CreateRepository(ctx context.Context, r backup.Repository, password secret.Sealed) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if backup.ValidateRepository(r) != nil || len(password.Nonce) != 12 || len(password.Ciphertext) < 16 || len(password.Ciphertext) > 4096 {
			return backup.ErrRepository
		}
		var conflict int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM repos WHERE id=? OR name=? OR (?='panel' AND role='panel')", r.ID, r.Name, r.Role).Scan(&conflict); err != nil {
			return ErrRepositoryMetadata
		}
		if conflict != 0 {
			return ErrRepositoryConflict
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO repos(id,name,kind,role,location,created) VALUES(?,?,?,?,?,?)", r.ID, r.Name, r.Kind, r.Role, r.Location, s.clock.Now()); err != nil {
			return ErrRepositoryMetadata
		}
		name := "repo:" + r.ID + ":password"
		if _, err := tx.ExecContext(ctx, "INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?)", name, name, password.Nonce, password.Ciphertext, s.clock.Now()); err != nil {
			return ErrRepositoryMetadata
		}
		return nil
	})
}

func (s *Store) ListRepositories(ctx context.Context) ([]backup.Repository, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,kind,role,location,retention_json,created FROM repos ORDER BY created,id")
	if err != nil {
		if errors.Is(err, ErrStoreClosed) {
			return nil, err
		}
		return nil, ErrRepositoryMetadata
	}
	defer rows.Close()
	out := []backup.Repository{}
	for rows.Next() {
		var r backup.Repository
		if err := rows.Scan(&r.ID, &r.Name, &r.Kind, &r.Role, &r.Location, &r.RetentionJSON, &r.Created); err != nil {
			return nil, ErrRepositoryMetadata
		}
		out = append(out, r)
	}
	if rows.Err() != nil {
		return nil, ErrRepositoryMetadata
	}
	return out, nil
}

// RepositoryPassword exposes ciphertext only to trusted agent execution code.
func (s *Store) RepositoryPassword(ctx context.Context, id string) (secret.Sealed, error) {
	var v secret.Sealed
	err := s.db.QueryRowContext(ctx, "SELECT nonce,ciphertext FROM secrets WHERE name=? AND EXISTS(SELECT 1 FROM repos WHERE id=?)", "repo:"+id+":password", id).Scan(&v.Nonce, &v.Ciphertext)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, ErrStoreClosed) {
		err = ErrRepositoryMetadata
	}
	return v, err
}

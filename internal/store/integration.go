package store

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/id"
)

func (s *Store) CreateIntegration(ctx context.Context, integrationID, config string, sealed ACMESealed) error {
	if !id.Valid(integrationID) || !validACMESealed(sealed) {
		return errors.New("invalid integration")
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		now := s.clock.Now()
		name := "integration:" + integrationID + ":token"
		if _, err := tx.ExecContext(ctx, "INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?)", name, name, sealed.Nonce, sealed.Ciphertext, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO integrations(id,kind,name,config_json,created) VALUES(?,?,?,?,?)", integrationID, "cloudflare", "cloudflare", config, now)
		return err
	})
}

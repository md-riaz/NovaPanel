package store

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/id"
)

func (s *Store) CreateIntegration(ctx context.Context, integrationID, config string, sealed ACMESealed, zones ...map[string]string) error {
	if !id.Valid(integrationID) || !validACMESealed(sealed) {
		return errors.New("invalid integration")
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		now := s.clock.Now()
		name := "integration:" + integrationID + ":token"
		if _, err := tx.ExecContext(ctx, "INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?)", name, name, sealed.Nonce, sealed.Ciphertext, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO integrations(id,kind,name,config_json,created) VALUES(?,?,?,?,?)", integrationID, "cloudflare", "cloudflare", config, now); err != nil {
			return err
		}
		for _, entries := range zones {
			for zone, providerID := range entries {
				if zone == "" || providerID == "" {
					return errors.New("invalid DNS zone")
				}
				zoneID, err := id.New()
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, "INSERT INTO dns_zones(id,integration_id,zone,provider_zone_id,created) VALUES(?,?,?,?,?)", zoneID, integrationID, zone, providerID, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

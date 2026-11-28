package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
)

var ErrZoneManaged = errors.New("zone managed by another integration")

// SetCloudflareIntegration rotates an existing zone owner's credential atomically.
func (s *Store) SetCloudflareIntegration(ctx context.Context, newID string, sealed ACMESealed, zones map[string]string) (string, error) {
	if !id.Valid(newID) || !validACMESealed(sealed) || len(zones) == 0 {
		return "", errors.New("invalid integration")
	}
	selected := ""
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		for zone, providerID := range zones {
			if zone == "" || providerID == "" {
				return errors.New("invalid DNS zone")
			}
			var owner, kind string
			err := tx.QueryRowContext(ctx, `SELECT z.integration_id,i.kind FROM dns_zones z JOIN integrations i ON i.id=z.integration_id WHERE z.zone=?`, zone).Scan(&owner, &kind)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if kind != "cloudflare" || (selected != "" && selected != owner) {
				return ErrZoneManaged
			}
			selected = owner
		}
		now := s.clock.Now()
		if selected == "" {
			selected = newID
			if _, err := tx.ExecContext(ctx, `INSERT INTO integrations(id,kind,name,config_json,created) VALUES(?,'cloudflare','cloudflare','{}',?)`, selected, now); err != nil {
				return err
			}
			name := "integration:" + selected + ":token"
			if _, err := tx.ExecContext(ctx, `INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?)`, name, name, sealed.Nonce, sealed.Ciphertext, now); err != nil {
				return err
			}
		} else {
			name := "integration:" + selected + ":token"
			result, err := tx.ExecContext(ctx, `UPDATE secrets SET nonce=?,ciphertext=?,created=? WHERE id=? AND name=?`, sealed.Nonce, sealed.Ciphertext, now, name, name)
			if err = affected(result, err); err != nil {
				return err
			}
		}
		for zone, providerID := range zones {
			var owner string
			err := tx.QueryRowContext(ctx, `SELECT integration_id FROM dns_zones WHERE zone=?`, zone).Scan(&owner)
			if err == nil {
				if owner != selected {
					return ErrZoneManaged
				}
				if _, err = tx.ExecContext(ctx, `UPDATE dns_zones SET provider_zone_id=? WHERE zone=? AND integration_id=?`, providerID, zone, selected); err != nil {
					return err
				}
			} else if errors.Is(err, sql.ErrNoRows) {
				zoneID, err := id.New()
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO dns_zones(id,integration_id,zone,provider_zone_id,created) VALUES(?,?,?,?,?)`, zoneID, selected, zone, providerID, now); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT zone FROM dns_zones WHERE integration_id=? ORDER BY zone`, selected)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			names = append(names, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		config, err := json.Marshal(struct {
			Kind  string   `json:"kind"`
			Zones []string `json:"zones"`
		}{"cloudflare", names})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE integrations SET config_json=? WHERE id=?`, string(config), selected)
		return err
	})
	if err != nil {
		return "", err
	}
	return selected, nil
}

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

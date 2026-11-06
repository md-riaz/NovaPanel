package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"novapanel/internal/dns"
	"novapanel/internal/id"
)

var ErrDNSIntegration = errors.New("DNS integration unavailable or invalid")

// IntegrationAuthority must come from resolved agent authority, never RPC fields.
// MFA and session/PAT expiry remain dispatcher/queue responsibilities.
type IntegrationAuthority struct {
	UserID    string
	LocalRoot bool
}
type IntegrationSealed struct {
	Nonce      []byte `json:"-"`
	Ciphertext []byte `json:"-"`
}

func (IntegrationSealed) Format(s fmt.State, _ rune)   { _, _ = io.WriteString(s, "[REDACTED]") }
func (IntegrationSealed) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (v IntegrationSealed) valid() bool {
	return len(v.Nonce) == 12 && len(v.Ciphertext) >= 16 && len(v.Ciphertext) <= 4112
}

type DNSIntegration struct {
	ID string `json:"integration_id"`
}

func integrationError(e error) error {
	if e == nil || errors.Is(e, ErrStoreClosed) || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return e
	}
	return ErrDNSIntegration
}
func integrationAuthority(ctx context.Context, tx *sql.Tx, a IntegrationAuthority) error {
	if a.LocalRoot {
		if a.UserID != "" {
			return ErrDNSIntegration
		}
		return nil
	}
	if a.UserID == "" {
		return ErrDNSIntegration
	}
	var n int
	if e := tx.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE id=? AND role IN ('admin','owner') AND disabled=0", a.UserID).Scan(&n); e != nil {
		return e
	}
	if n != 1 {
		return ErrDNSIntegration
	}
	return nil
}
func integrationOwner(ctx context.Context, tx *sql.Tx, a IntegrationAuthority, integrationID string) error {
	if e := integrationAuthority(ctx, tx, a); e != nil {
		return e
	}
	var owner sql.NullString
	var root bool
	if e := tx.QueryRowContext(ctx, "SELECT user_id,local_root FROM integration_owners WHERE integration_id=?", integrationID).Scan(&owner, &root); e != nil {
		return e
	}
	if !a.LocalRoot && (root || !owner.Valid || owner.String != a.UserID) {
		return ErrDNSIntegration
	}
	return nil
}

// CreateDNSIntegration publishes a fresh lifetime and its sealed credential atomically.
// Scope probing must succeed before calling this method; ciphertext is opaque here.
// These APIs deliberately require proposed scratch-only Phase 7 tables.
func (s *Store) CreateDNSIntegration(ctx context.Context, a IntegrationAuthority, spec dns.IntegrationSpec, sealed IntegrationSealed) (DNSIntegration, error) {
	var out DNSIntegration
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if spec.Validate() != nil || !sealed.valid() {
			return ErrDNSIntegration
		}
		// Take the write lock before checking authority or sampling publication time.
		if _, e := tx.ExecContext(ctx, "UPDATE integrations SET id=id WHERE 0"); e != nil {
			return e
		}
		if e := integrationAuthority(ctx, tx, a); e != nil {
			return e
		}
		integrationID, e := id.New()
		if e != nil {
			return e
		}
		now := s.clock.Now()
		if _, e = tx.ExecContext(ctx, "INSERT INTO integrations(id,kind,name,config_json,created) VALUES(?,? ,?,'{}',?)", integrationID, spec.Kind, "cloudflare:"+integrationID, now); e != nil {
			return e
		}
		var owner any
		if !a.LocalRoot {
			owner = a.UserID
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO integration_owners(integration_id,user_id,local_root) VALUES(?,?,?)", integrationID, owner, a.LocalRoot); e != nil {
			return e
		}
		name := "integration:" + integrationID + ":token"
		if _, e = tx.ExecContext(ctx, "INSERT INTO secrets(id,name,nonce,ciphertext,created) VALUES(?,?,?,?,?)", integrationID, name, sealed.Nonce, sealed.Ciphertext, now); e != nil {
			return e
		}
		for _, z := range spec.Zones {
			zoneID, e := id.New()
			if e != nil {
				return e
			}
			// Canonical zones may occur in multiple explicitly bound integrations.
			if _, e = tx.ExecContext(ctx, "INSERT INTO dns_zones(id,integration_id,zone,provider_zone_id) VALUES(?,?,?,?)", zoneID, integrationID, z.Zone, z.ProviderZoneID); e != nil {
				return e
			}
		}
		out.ID = integrationID
		return nil
	})
	if e != nil {
		return DNSIntegration{}, integrationError(e)
	}
	return out, nil
}

// DNSIntegrationToken requires both the exact durable lifetime and its zone membership.
// The caller opens ciphertext only while holding the future queue's zone lock.
func (s *Store) DNSIntegrationToken(ctx context.Context, a IntegrationAuthority, zone, integrationID string) (IntegrationSealed, error) {
	var out IntegrationSealed
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		if !id.Valid(integrationID) {
			return ErrDNSIntegration
		}
		if e := integrationOwner(ctx, tx, a, integrationID); e != nil {
			return e
		}
		var kind, providerID string
		if e := tx.QueryRowContext(ctx, "SELECT i.kind,z.provider_zone_id FROM integrations i JOIN dns_zones z ON z.integration_id=i.id WHERE i.id=? AND z.zone=?", integrationID, zone).Scan(&kind, &providerID); e != nil {
			return e
		}
		if (dns.IntegrationSpec{Kind: kind, Zones: []dns.ZoneBinding{{Zone: zone, ProviderZoneID: providerID}}}).Validate() != nil {
			return ErrDNSIntegration
		}
		if e := tx.QueryRowContext(ctx, "SELECT nonce,ciphertext FROM secrets WHERE name=?", "integration:"+integrationID+":token").Scan(&out.Nonce, &out.Ciphertext); e != nil {
			return e
		}
		if !out.valid() {
			return ErrDNSIntegration
		}
		return nil
	})
	if e != nil {
		return IntegrationSealed{}, integrationError(e)
	}
	return out, nil
}
func (s *Store) DeleteDNSIntegration(ctx context.Context, a IntegrationAuthority, integrationID string) error {
	return integrationError(s.Tx(ctx, func(tx *sql.Tx) error {
		if !id.Valid(integrationID) {
			return ErrDNSIntegration
		}
		if _, e := tx.ExecContext(ctx, "UPDATE integrations SET id=id WHERE 0"); e != nil {
			return e
		}
		if e := integrationOwner(ctx, tx, a, integrationID); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM secrets WHERE name=?", "integration:"+integrationID+":token"); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "DELETE FROM integrations WHERE id=?", integrationID)
		return e
	}))
}

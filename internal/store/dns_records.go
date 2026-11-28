package store

import (
	"context"
	"database/sql"
	"errors"
	"novapanel/internal/dns"
	"novapanel/internal/id"
)

type DNSZone struct{ ID, IntegrationID, Zone, ProviderZoneID string }
type DNSRecord struct {
	ObjID, ZoneID, Type, Name, Value string
	TTL, Priority                    int
	Proxied                          bool
	ProviderRecordID                 sql.NullString
	ConflictError                    sql.NullString
	Phase                            string
	Reconciled                       bool
	Created, Updated                 int64
}

// DNSZoneByName fails closed if multiple integrations claim the same zone.
func (s *Store) DNSZoneByName(ctx context.Context, name string) (DNSZone, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,integration_id,zone,provider_zone_id FROM dns_zones WHERE zone=? LIMIT 2`, name)
	if err != nil {
		return DNSZone{}, err
	}
	defer rows.Close()
	var z DNSZone
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return z, err
		}
		return z, sql.ErrNoRows
	}
	if err := rows.Scan(&z.ID, &z.IntegrationID, &z.Zone, &z.ProviderZoneID); err != nil {
		return z, err
	}
	if rows.Next() {
		return DNSZone{}, ErrDNSOwnership
	}
	return z, rows.Err()
}
func (s *Store) DNSZoneByID(ctx context.Context, zoneID string) (DNSZone, error) {
	var z DNSZone
	err := s.db.QueryRowContext(ctx, `SELECT id,integration_id,zone,provider_zone_id FROM dns_zones WHERE id=?`, zoneID).Scan(&z.ID, &z.IntegrationID, &z.Zone, &z.ProviderZoneID)
	return z, err
}
func (s *Store) DNSCredential(ctx context.Context, integrationID string) (ACMESealed, error) {
	var sealed ACMESealed
	err := s.db.QueryRowContext(ctx, `SELECT nonce,ciphertext FROM secrets WHERE name=?`, "integration:"+integrationID+":token").Scan(&sealed.Nonce, &sealed.Ciphertext)
	return sealed, err
}
func (s *Store) DNSRecordsInZone(ctx context.Context, zoneID string) ([]DNSRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT obj_id,zone_id,type,name,value,ttl,priority,proxied,provider_record_id,phase,reconciled,conflict_error,created,updated FROM dns_records WHERE zone_id=?`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DNSRecord
	for rows.Next() {
		var r DNSRecord
		if err := rows.Scan(&r.ObjID, &r.ZoneID, &r.Type, &r.Name, &r.Value, &r.TTL, &r.Priority, &r.Proxied, &r.ProviderRecordID, &r.Phase, &r.Reconciled, &r.ConflictError, &r.Created, &r.Updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReserveDNSRecord records intent before the provider is called.
func (s *Store) ReserveDNSRecord(ctx context.Context, r DNSRecord) error {
	if !id.Valid(r.ObjID) || !id.Valid(r.ZoneID) {
		return errors.New("invalid DNS identity")
	}
	now := s.clock.Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO dns_records(obj_id,zone_id,type,name,value,ttl,priority,proxied,phase,reconciled,created,updated) VALUES(?,?,?,?,?,?,?,?,'reserved',0,?,?)`, r.ObjID, r.ZoneID, r.Type, r.Name, r.Value, r.TTL, r.Priority, r.Proxied, now, now)
	return err
}

func (s *Store) DNSRecord(ctx context.Context, objID string) (DNSRecord, error) {
	var r DNSRecord
	err := s.db.QueryRowContext(ctx, `SELECT obj_id,zone_id,type,name,value,ttl,priority,proxied,provider_record_id,phase,reconciled,conflict_error,created,updated FROM dns_records WHERE obj_id=?`, objID).Scan(&r.ObjID, &r.ZoneID, &r.Type, &r.Name, &r.Value, &r.TTL, &r.Priority, &r.Proxied, &r.ProviderRecordID, &r.Phase, &r.Reconciled, &r.ConflictError, &r.Created, &r.Updated)
	return r, err
}

func (s *Store) ConfirmDNSRecord(ctx context.Context, objID, providerID string) error {
	if providerID == "" {
		return errors.New("empty DNS provider identity")
	}
	r, err := s.db.ExecContext(ctx, `UPDATE dns_records SET provider_record_id=?,phase='created',reconciled=1,updated=? WHERE obj_id=? AND phase='reserved'`, providerID, s.clock.Now(), objID)
	return affected(r, err)
}

func (s *Store) MarkDNSRecordDeleting(ctx context.Context, objID string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE dns_records SET phase='deleting',reconciled=0,updated=? WHERE obj_id=? AND phase IN ('reserved','created','deleting')`, s.clock.Now(), objID)
	return affected(r, err)
}

func (s *Store) RemoveDNSRecord(ctx context.Context, objID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM dns_records WHERE obj_id=? AND phase='deleting'`, objID)
	return err
}

var ErrDNSOwnership = errors.New("DNS record ownership conflict")

func dnsComment(objID string) string { return "novapanel:obj_id=" + objID }

// ReconcileDNSCreate resolves a reserved create using the provider's ownership stamp.
func (s *Store) ReconcileDNSCreate(ctx context.Context, p dns.Provider, zone string, r DNSRecord) error {
	records, err := p.List(ctx, zone)
	if err != nil {
		return err
	}
	var own *dns.Record
	for i := range records {
		v := &records[i]
		if v.Name != r.Name {
			continue
		}
		if v.Comment != dnsComment(r.ObjID) || own != nil || v.ID == "" {
			return ErrDNSOwnership
		}
		own = v
	}
	if r.Phase == "created" {
		if own == nil || !r.ProviderRecordID.Valid || own.ID != r.ProviderRecordID.String {
			return ErrDNSOwnership
		}
		return nil
	}
	if r.Phase != "reserved" {
		return ErrDNSOwnership
	}
	providerID := ""
	if own != nil {
		providerID = own.ID
		desired := dns.Record{ID: own.ID, Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Priority: r.Priority, Proxied: r.Proxied, Comment: dnsComment(r.ObjID)}
		if *own != desired {
			if err = p.Update(ctx, zone, desired); err != nil {
				return err
			}
		}
	} else {
		providerID, err = p.Create(ctx, zone, dns.Record{Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Priority: r.Priority, Proxied: r.Proxied, Comment: dnsComment(r.ObjID)})
		if err != nil {
			return err
		}
	}
	return s.ConfirmDNSRecord(ctx, r.ObjID, providerID)
}

// DeleteManagedDNSRecord verifies the provider identity before removing it.
func (s *Store) DeleteManagedDNSRecord(ctx context.Context, p dns.Provider, zone string, r DNSRecord) error {
	if r.Phase != "deleting" {
		if err := s.MarkDNSRecordDeleting(ctx, r.ObjID); err != nil {
			return err
		}
	}
	records, err := p.List(ctx, zone)
	if err != nil {
		return err
	}
	var owned *dns.Record
	for i := range records {
		v := &records[i]
		if r.ProviderRecordID.Valid {
			if v.ID != r.ProviderRecordID.String {
				continue
			}
		} else if v.Name != r.Name {
			continue
		}
		if v.Comment != dnsComment(r.ObjID) || owned != nil || v.ID == "" {
			return ErrDNSOwnership
		}
		owned = v
	}
	if owned != nil {
		if err := p.Delete(ctx, zone, *owned); err != nil {
			return err
		}
	}
	return s.RemoveDNSRecord(ctx, r.ObjID)
}

// UpdateManagedDNSRecord verifies identity before applying the requested state.
func (s *Store) UpdateManagedDNSRecord(ctx context.Context, p dns.Provider, zone string, r DNSRecord) error {
	if r.Phase != "updating" || !r.ProviderRecordID.Valid {
		return ErrDNSOwnership
	}
	records, err := p.List(ctx, zone)
	if err != nil {
		return err
	}
	for _, v := range records {
		if v.ID != r.ProviderRecordID.String {
			continue
		}
		if v.Comment != dnsComment(r.ObjID) {
			return ErrDNSOwnership
		}
		desired := dns.Record{ID: v.ID, Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Priority: r.Priority, Proxied: r.Proxied, Comment: dnsComment(r.ObjID)}
		if v != desired {
			if err := p.Update(ctx, zone, desired); err != nil {
				return err
			}
		}
		return s.CompleteDNSUpdate(ctx, r.ObjID, r.ProviderRecordID.String)
	}
	return ErrDNSOwnership
}

// ReconcileDNSRecord applies a pending desired state or preserves an ownership conflict.
func (s *Store) ReconcileDNSRecord(ctx context.Context, p dns.Provider, zone string, r DNSRecord) error {
	var err error
	switch r.Phase {
	case "reserved":
		err = s.ReconcileDNSCreate(ctx, p, zone, r)
	case "updating":
		err = s.UpdateManagedDNSRecord(ctx, p, zone, r)
	case "deleting":
		err = s.DeleteManagedDNSRecord(ctx, p, zone, r)
	default:
		return errors.New("invalid pending DNS phase")
	}
	if errors.Is(err, ErrDNSOwnership) {
		if e := s.MarkDNSConflict(ctx, r.ObjID); e != nil {
			return e
		}
	}
	return err
}

// BeginDNSUpdate persists the desired record before contacting the provider.
func (s *Store) BeginDNSUpdate(ctx context.Context, r DNSRecord) error {
	result, err := s.db.ExecContext(ctx, `UPDATE dns_records SET type=?,name=?,value=?,ttl=?,priority=?,proxied=?,phase='updating',reconciled=0,updated=? WHERE obj_id=? AND provider_record_id=? AND phase='created' AND reconciled=1 AND conflict_error IS NULL`, r.Type, r.Name, r.Value, r.TTL, r.Priority, r.Proxied, s.clock.Now(), r.ObjID, r.ProviderRecordID)
	return affected(result, err)
}

func (s *Store) CompleteDNSUpdate(ctx context.Context, objID, providerID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE dns_records SET phase='created',reconciled=1,updated=? WHERE obj_id=? AND provider_record_id=? AND phase='updating' AND conflict_error IS NULL`, s.clock.Now(), objID, providerID)
	return affected(result, err)
}

func (s *Store) DNSPendingRecords(ctx context.Context) ([]DNSRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT obj_id,zone_id,type,name,value,ttl,priority,proxied,provider_record_id,phase,reconciled,conflict_error,created,updated FROM dns_records WHERE reconciled=0 AND conflict_error IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DNSRecord
	for rows.Next() {
		var r DNSRecord
		if err := rows.Scan(&r.ObjID, &r.ZoneID, &r.Type, &r.Name, &r.Value, &r.TTL, &r.Priority, &r.Proxied, &r.ProviderRecordID, &r.Phase, &r.Reconciled, &r.ConflictError, &r.Created, &r.Updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) MarkDNSConflict(ctx context.Context, objID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE dns_records SET conflict_error='ownership conflict',updated=? WHERE obj_id=? AND reconciled=0`, s.clock.Now(), objID)
	return affected(result, err)
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"novapanel/internal/dns"
	"novapanel/internal/id"
	"novapanel/internal/secret"
)

func dnsMatrixSetup(t *testing.T) (context.Context, *Store, *dns.Fake, DNSRecord) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	sealer, err := secret.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal([]byte("token"))
	if err != nil {
		t.Fatal(err)
	}
	integration, _ := id.New()
	if err = s.CreateIntegration(ctx, integration, "{}", ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, map[string]string{"example.com": "zone-id"}); err != nil {
		t.Fatal(err)
	}
	zone, err := s.DNSZoneByName(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	obj, _ := id.New()
	r := DNSRecord{ObjID: obj, ZoneID: zone.ID, Type: "A", Name: "www.example.com", Value: "192.0.2.1", TTL: 1}
	if err = s.ReserveDNSRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, err = s.DNSRecord(ctx, obj)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, &dns.Fake{}, r
}
func matrixRecord(r DNSRecord) dns.Record {
	return dns.Record{ID: r.ProviderRecordID.String, Type: r.Type, Name: r.Name, Value: r.Value, TTL: r.TTL, Priority: r.Priority, Proxied: r.Proxied, Comment: dnsComment(r.ObjID)}
}
func TestDNSCrashReplayMatrix(t *testing.T) {
	for _, stop := range []string{"reserved", "provider_created", "created"} {
		t.Run("create_"+stop, func(t *testing.T) {
			ctx, s, p, r := dnsMatrixSetup(t)
			if stop != "reserved" {
				if _, err := p.Create(ctx, "example.com", matrixRecord(r)); err != nil {
					t.Fatal(err)
				}
			}
			if stop == "created" {
				records, _ := p.List(ctx, "example.com")
				if err := s.ConfirmDNSRecord(ctx, r.ObjID, records[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			r, err := s.DNSRecord(ctx, r.ObjID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Phase == "reserved" {
				err = s.ReconcileDNSRecord(ctx, p, "example.com", r)
			} else {
				err = s.ReconcileDNSCreate(ctx, p, "example.com", r)
			}
			if err != nil {
				t.Fatal(err)
			}
			records, err := p.List(ctx, "example.com")
			if err != nil || len(records) != 1 || records[0].Comment != dnsComment(r.ObjID) {
				t.Fatal("create replay duplicated or adopted record")
			}
			r, err = s.DNSRecord(ctx, r.ObjID)
			if err != nil || r.Phase != "created" || !r.Reconciled {
				t.Fatal("create did not settle")
			}
		})
	}
	for _, stop := range []string{"intent", "provider_updated", "committed"} {
		t.Run("update_"+stop, func(t *testing.T) {
			ctx, s, p, r := dnsMatrixSetup(t)
			if err := s.ReconcileDNSRecord(ctx, p, "example.com", r); err != nil {
				t.Fatal(err)
			}
			r, err := s.DNSRecord(ctx, r.ObjID)
			if err != nil {
				t.Fatal(err)
			}
			r.Value = "192.0.2.2"
			if err = s.BeginDNSUpdate(ctx, r); err != nil {
				t.Fatal(err)
			}
			r, err = s.DNSRecord(ctx, r.ObjID)
			if err != nil {
				t.Fatal(err)
			}
			if stop != "intent" {
				if err = p.Update(ctx, "example.com", matrixRecord(r)); err != nil {
					t.Fatal(err)
				}
			}
			if stop == "committed" {
				if err = s.CompleteDNSUpdate(ctx, r.ObjID, r.ProviderRecordID.String); err != nil {
					t.Fatal(err)
				}
			}
			r, err = s.DNSRecord(ctx, r.ObjID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Phase == "updating" {
				if err = s.ReconcileDNSRecord(ctx, p, "example.com", r); err != nil {
					t.Fatal(err)
				}
			}
			records, err := p.List(ctx, "example.com")
			if err != nil || len(records) != 1 || records[0].Value != "192.0.2.2" || records[0].Comment != dnsComment(r.ObjID) {
				t.Fatal("update replay changed wrong record")
			}
			r, err = s.DNSRecord(ctx, r.ObjID)
			if err != nil || r.Phase != "created" || !r.Reconciled {
				t.Fatal("update did not settle")
			}
		})
	}
	for _, stop := range []string{"intent", "provider_deleted", "row_deleted"} {
		t.Run("delete_"+stop, func(t *testing.T) {
			ctx, s, p, r := dnsMatrixSetup(t)
			if err := s.ReconcileDNSRecord(ctx, p, "example.com", r); err != nil {
				t.Fatal(err)
			}
			r, err := s.DNSRecord(ctx, r.ObjID)
			if err != nil {
				t.Fatal(err)
			}
			foreign := dns.Record{ID: "foreign", Type: "TXT", Name: "other.example.com", Value: "keep", TTL: 1, Comment: "novapanel:obj_id=foreign"}
			if _, err = p.Create(ctx, "example.com", foreign); err != nil {
				t.Fatal(err)
			}
			if err = s.MarkDNSRecordDeleting(ctx, r.ObjID); err != nil {
				t.Fatal(err)
			}
			if stop != "intent" {
				if err = p.Delete(ctx, "example.com", matrixRecord(r)); err != nil {
					t.Fatal(err)
				}
			}
			if stop == "row_deleted" {
				if err = s.RemoveDNSRecord(ctx, r.ObjID); err != nil {
					t.Fatal(err)
				}
			}
			objID := r.ObjID
			r, err = s.DNSRecord(ctx, objID)
			if err == nil {
				if err = s.ReconcileDNSRecord(ctx, p, "example.com", r); err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatal(err)
			}
			records, err := p.List(ctx, "example.com")
			if err != nil || len(records) != 1 || records[0] != foreign {
				t.Fatal("delete removed foreign record")
			}
			if _, err = s.DNSRecord(ctx, objID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("delete row remains")
			}
		})
	}
}
func TestDNSCreateReplayRestoresDesiredRecord(t *testing.T) {
	ctx, s, p, r := dnsMatrixSetup(t)
	drift := matrixRecord(r)
	drift.Value = "192.0.2.99"
	drift.TTL = 300
	providerID, err := p.Create(ctx, "example.com", drift)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileDNSRecord(ctx, p, "example.com", r); err != nil {
		t.Fatal(err)
	}
	records, err := p.List(ctx, "example.com")
	want := matrixRecord(r)
	want.ID = providerID
	if err != nil || len(records) != 1 || records[0] != want {
		t.Fatal("replay did not restore desired record")
	}
	saved, err := s.DNSRecord(ctx, r.ObjID)
	if err != nil || !saved.Reconciled || saved.ProviderRecordID.String != providerID {
		t.Fatal("record not confirmed")
	}
}

func TestDNSForeignStampStopsSweep(t *testing.T) {
	for _, phase := range []string{"reserved", "updating", "deleting"} {
		t.Run(phase, func(t *testing.T) {
			ctx, s, p, r := dnsMatrixSetup(t)
			if phase != "reserved" {
				if err := s.ReconcileDNSRecord(ctx, p, "example.com", r); err != nil {
					t.Fatal(err)
				}
				var err error
				r, err = s.DNSRecord(ctx, r.ObjID)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "updating" {
					r.Value = "192.0.2.2"
					err = s.BeginDNSUpdate(ctx, r)
				} else {
					err = s.MarkDNSRecordDeleting(ctx, r.ObjID)
				}
				if err != nil {
					t.Fatal(err)
				}
				r, err = s.DNSRecord(ctx, r.ObjID)
				if err != nil {
					t.Fatal(err)
				}
				foreign := matrixRecord(r)
				foreign.Comment = "novapanel:obj_id=another"
				if err = p.Update(ctx, "example.com", foreign); err != nil {
					t.Fatal(err)
				}
			} else {
				foreign := matrixRecord(r)
				foreign.ID = "foreign"
				foreign.Comment = "novapanel:obj_id=another"
				if _, err := p.Create(ctx, "example.com", foreign); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ReconcileDNSRecord(ctx, p, "example.com", r); !errors.Is(err, ErrDNSOwnership) {
				t.Fatalf("missing conflict: %v", err)
			}
			rows, err := s.DNSPendingRecords(ctx)
			if err != nil || len(rows) != 0 {
				t.Fatal("conflict retried")
			}
			saved, err := s.DNSRecord(ctx, r.ObjID)
			if err != nil || !saved.ConflictError.Valid {
				t.Fatal("conflict not surfaced")
			}
			records, err := p.List(ctx, "example.com")
			if err != nil || len(records) != 1 || records[0].Comment != "novapanel:obj_id=another" {
				t.Fatal("foreign record changed")
			}
		})
	}
}

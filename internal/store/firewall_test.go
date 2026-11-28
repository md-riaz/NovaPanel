package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestFirewallPendingAdmission(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	p := FirewallPending{ID: "one", SnapshotPath: "/snapshot", RevertUnit: "revert.timer", Deadline: 42}
	if err = s.ReserveFirewall(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveFirewall(ctx, FirewallPending{ID: "two"}); !errors.Is(err, ErrSiteConflict) {
		t.Fatalf("second reservation: %v", err)
	}
	got, err := s.FirewallPending(ctx)
	if err != nil || got != p {
		t.Fatalf("pending: %+v %v", got, err)
	}
	if err = s.DeleteFirewallPending(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FirewallPending(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("after delete: %v", err)
	}
}

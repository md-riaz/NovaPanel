package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestACMEReservationPersistsAndRolls(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	now := int64(2000000000000)
	week := int64((7 * 24 * time.Hour) / time.Millisecond)
	buckets := []ACMEBucket{{Key: "staging:domain:example.com", Limit: 50, Window: week}, {Key: "staging:set:abc", Limit: 5, Window: week}}
	for i := 0; i < 5; i++ {
		if e = s.ReserveACME(ctx, buckets, now); e != nil {
			t.Fatal(e)
		}
	}
	s.Close()
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.ReserveACME(ctx, buckets, now+week-1); !errors.Is(e, ErrACMERateLimited) {
		t.Fatalf("exhaustion: %v", e)
	}
	if e = s.ReserveACME(ctx, buckets, now-1); !errors.Is(e, ErrACMERateLimited) {
		t.Fatalf("clock rollback: %v", e)
	}
	if e = s.ReserveACME(ctx, buckets, now+week); e != nil {
		t.Fatalf("rollover: %v", e)
	}
}

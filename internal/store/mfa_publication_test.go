package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPendingMFATimestampAtPublication(t *testing.T) {
	const ttl int64 = 300000
	for _, age := range []int64{ttl - 1, ttl} {
		name := "before expiry"
		if age == ttl {
			name = "at expiry"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			clock := &testClock{}
			clock.now.Store(1800000000000)
			s, err := OpenWithClock(":memory:", clock)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.CreateUser(ctx, User{ID: "u", Username: "owner", PWPHC: "phc", Role: "owner"}); err != nil {
				t.Fatal(err)
			}
			conn, err := s.db.raw.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			waits := s.db.raw.Stats().WaitCount
			result := make(chan error, 1)
			go func() { result <- s.SetPendingMFA(ctx, "u", "sealed.pending") }()
			for s.db.raw.Stats().WaitCount == waits {
				select {
				case <-ctx.Done():
					t.Fatal("pending enrollment did not wait for the connection")
				case <-time.After(time.Millisecond):
				}
			}
			published := clock.now.Load() + ttl + 1
			clock.now.Store(published)
			if err = conn.Close(); err != nil {
				t.Fatal(err)
			}
			if err = <-result; err != nil {
				t.Fatal(err)
			}
			u, err := s.UserByID(ctx, "u")
			if err != nil {
				t.Fatal(err)
			}
			if u.TOTPPendingAt == nil || *u.TOTPPendingAt != published || u.Updated != published {
				t.Fatalf("pending timestamp = %v, updated = %d; want publication %d", u.TOTPPendingAt, u.Updated, published)
			}
			codes := make([]BackupCode, 0, 8)
			for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
				codes = append(codes, BackupCode{ID: id, UserID: "u", CodePHC: "phc"})
			}
			clock.now.Store(published + age)
			err = s.ConfirmMFA(ctx, "u", "sealed.pending", clock.now.Load()/30000, ttl, codes)
			if age < ttl && err != nil {
				t.Fatal("freshly published enrollment expired early:", err)
			}
			if age == ttl && !errors.Is(err, ErrAuthExpired) {
				t.Fatal("enrollment did not expire at publication TTL:", err)
			}
		})
	}
}

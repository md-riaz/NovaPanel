package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

type authClock struct{ now int64 }

func (c *authClock) Now() int64 { return c.now }
func TestMFAPersistentPromotionReplayBackup(t *testing.T) {
	ctx := context.Background()
	clock := &authClock{1800000000000}
	path := filepath.Join(t.TempDir(), "state.db")
	s, e := OpenWithClock(path, clock)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	if e = s.CreateUser(ctx, User{ID: "u", Username: "owner", Email: "e", PWPHC: "hash", Role: "owner"}); e != nil {
		t.Fatal(e)
	}
	if e = s.SetPendingMFA(ctx, "u", "sealed.pending"); e != nil {
		t.Fatal(e)
	}
	step := clock.now / 30000
	codes := []BackupCode{{ID: "backup", UserID: "u", CodePHC: "phc"}}
	for _, id := range []string{"b2", "b3", "b4", "b5", "b6", "b7", "b8"} {
		codes = append(codes, BackupCode{ID: id, UserID: "u", CodePHC: "phc"})
	}
	if e = s.ConfirmMFA(ctx, "u", "other", step, 300000, codes); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	if e = s.ConfirmMFA(ctx, "u", "sealed.pending", step, 300000, codes); e != nil {
		t.Fatal(e)
	}
	u, e := s.UserByID(ctx, "u")
	if e != nil || u.TOTPSecret == nil || *u.TOTPSecret != "sealed.pending" || u.TOTPPending != nil || u.TOTPPendingAt != nil {
		t.Fatal(u, e)
	}
	if e = s.SetPendingMFA(ctx, "u", "replacement"); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenWithClock(path, clock)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ConsumeTOTP(ctx, "u", "sealed.pending", step); !errors.Is(e, ErrAuthConflict) {
		t.Fatal("replay", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { results <- s.ConsumeTOTP(ctx, "u", "sealed.pending", step+1) })
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else if !errors.Is(e, ErrAuthConflict) {
			t.Fatal(e)
		}
	}
	if successes != 1 {
		t.Fatal(successes)
	}
	if e = s.ConsumeBackupCode(ctx, "u", "backup", "phc"); e != nil {
		t.Fatal(e)
	}
	if e = s.ConsumeBackupCode(ctx, "u", "backup", "phc"); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	if e = s.DisableMFA(ctx, "u", "sealed.pending"); e != nil {
		t.Fatal(e)
	}
	if e = s.ConsumeTOTP(ctx, "u", "sealed.pending", step-1); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
}

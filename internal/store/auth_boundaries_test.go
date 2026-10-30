package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestSessionTimeoutRotationRollbackAndPassword(t *testing.T) {
	ctx := context.Background()
	c := &authClock{1800000000000}
	s, e := OpenWithClock(":memory:", c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	uid, role := "u", "owner"
	if e = s.CreateUser(ctx, User{ID: uid, Username: "owner", Email: "e", PWPHC: "old", Role: role}); e != nil {
		t.Fatal(e)
	}
	ss := Session{ID: "s1", TokenHash: "hash1", Kind: "user", UserID: &uid, Role: &role, CSRFToken: "csrf1", IdleDeadline: c.now + 600000, AbsDeadline: c.now + 43200000}
	if e = s.CreateSession(ctx, ss); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TouchSession(ctx, ss.ID, "wrong", 600000); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	c.now += 100
	got, e := s.TouchSession(ctx, ss.ID, ss.TokenHash, 600000)
	if e != nil || got.IdleDeadline != c.now+600000 || got.AbsDeadline != ss.AbsDeadline {
		t.Fatal(got, e)
	}
	replacement := ss
	replacement.ID = "s2"
	replacement.TokenHash = "hash2"
	replacement.CSRFToken = "csrf2"
	if e = s.CreateSession(ctx, replacement); e != nil {
		t.Fatal(e)
	}
	if e = s.RotateSession(ctx, ss.ID, replacement); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	if _, e = s.GetSession(ctx, ss.ID); e != nil {
		t.Fatal("failed rotate deleted original", e)
	}
	replacement.ID = "s3"
	if e = s.RotateSession(ctx, ss.ID, replacement); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetSession(ctx, ss.ID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	if e = s.RehashPassword(ctx, uid, "wrong", "new"); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	if e = s.RehashPassword(ctx, uid, "old", "new"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetSession(ctx, replacement.ID); e != nil {
		t.Fatal("rehash revoked session", e)
	}
	if e = s.SetPassword(ctx, uid, "reset"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetSession(ctx, replacement.ID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal(e)
	}
	ss.ID = "expiry"
	ss.IdleDeadline = c.now + 1000
	ss.AbsDeadline = c.now + 2000
	if e = s.CreateSession(ctx, ss); e != nil {
		t.Fatal(e)
	}
	c.now += 1000
	if _, e = s.GetSession(ctx, ss.ID); !errors.Is(e, ErrAuthExpired) {
		t.Fatal("idle boundary", e)
	}
	if _, e = s.TouchSession(ctx, ss.ID, ss.TokenHash, 600000); !errors.Is(e, ErrAuthExpired) {
		t.Fatal("revived expired", e)
	}
	ss.ID = "absolute"
	ss.AbsDeadline = c.now + 1000
	ss.IdleDeadline = ss.AbsDeadline
	if e = s.CreateSession(ctx, ss); e != nil {
		t.Fatal(e)
	}
	c.now += 500
	got, e = s.TouchSession(ctx, ss.ID, ss.TokenHash, 600000)
	if e != nil || got.IdleDeadline != ss.AbsDeadline {
		t.Fatal(got, e)
	}
	c.now = ss.AbsDeadline
	if _, e = s.GetSession(ctx, ss.ID); !errors.Is(e, ErrAuthExpired) {
		t.Fatal("absolute boundary", e)
	}
	if e = s.DeleteSession(ctx, ss.ID); e != nil {
		t.Fatal(e)
	}
	disabled := true
	if e = s.UpdateUser(ctx, uid, UserPatch{Disabled: &disabled}); e != nil {
		t.Fatal(e)
	}
	ss.ID = "disabled"
	ss.IdleDeadline = c.now + 1000
	ss.AbsDeadline = c.now + 2000
	if e = s.CreateSession(ctx, ss); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
}
func TestMFAPendingExpiryRollbackAndSkew(t *testing.T) {
	ctx := context.Background()
	c := &authClock{1800000000000}
	s, e := OpenWithClock(":memory:", c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.CreateUser(ctx, User{ID: "u", Username: "owner", Email: "e", PWPHC: "phc", Role: "owner"}); e != nil {
		t.Fatal(e)
	}
	codes := []BackupCode{}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		codes = append(codes, BackupCode{ID: id, UserID: "u", CodePHC: "phc"})
	}
	if e = s.SetPendingMFA(ctx, "u", "sealed"); e != nil {
		t.Fatal(e)
	}
	c.now += 300000
	if e = s.ConfirmMFA(ctx, "u", "sealed", c.now/30000, 300000, codes); !errors.Is(e, ErrAuthExpired) {
		t.Fatal(e)
	}
	if e = s.SetPendingMFA(ctx, "u", "newsealed"); e != nil {
		t.Fatal(e)
	}
	step := c.now / 30000
	if e = s.ConfirmMFA(ctx, "u", "newsealed", step+2, 300000, codes); !errors.Is(e, ErrAuthExpired) {
		t.Fatal(e)
	}
	bad := append([]BackupCode(nil), codes...)
	bad[7].ID = bad[0].ID
	if e = s.ConfirmMFA(ctx, "u", "newsealed", step, 300000, bad); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	u, e := s.UserByID(ctx, "u")
	if e != nil || u.TOTPSecret != nil || u.TOTPPending == nil {
		t.Fatal("partial promotion", u, e)
	}
	if e = s.ConfirmMFA(ctx, "u", "newsealed", step, 300000, codes); e != nil {
		t.Fatal("rolled back replay", e)
	}
	if e = s.ConsumeTOTP(ctx, "u", "newsealed", step-1); e != nil {
		t.Fatal("negative skew", e)
	}
	if e = s.ConsumeTOTP(ctx, "u", "oldsealed", step+1); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	if e = s.DisableMFA(ctx, "u", "oldsealed"); !errors.Is(e, ErrAuthConflict) {
		t.Fatal(e)
	}
	c.now += 90000
	if e = s.ConsumeTOTP(ctx, "u", "newsealed", c.now/30000); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.db.QueryRowContext(ctx, "SELECT count(*) FROM mfa_used WHERE user_id=?", "u").Scan(&n); e != nil || n != 1 {
		t.Fatal("prune", n, e)
	}
}

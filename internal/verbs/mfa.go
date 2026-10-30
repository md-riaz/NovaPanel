package verbs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/auth"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"time"
)

func init() {
	rpc.Register(rpc.Verb{Name: "mfa.enroll", MinRole: rpc.ReadOnly, NewArgs: func() any { return &emptyArgs{} }, Run: mfaEnroll})
	rpc.Register(rpc.Verb{Name: "mfa.confirm", MinRole: rpc.ReadOnly, NewArgs: func() any { return &reauthArgs{} }, Run: mfaConfirm})
	rpc.Register(rpc.Verb{Name: "mfa.disable", MinRole: rpc.ReadOnly, MFA: true, NewArgs: func() any { return &reauthArgs{} }, Run: mfaDisable})
}
func mfaEnroll(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	u, e := a.Store.UserByID(ctx, rpc.CallerFrom(ctx).UserID)
	if e != nil {
		return nil, e
	}
	if u.TOTPSecret != nil {
		return nil, rpc.Errf("conflict", "MFA already enrolled")
	}
	if a.Secret == nil {
		return nil, rpc.Errf("agent_error", "MFA unavailable")
	}
	key, uri, e := auth.GenerateTOTP(u.Username)
	if e != nil {
		return nil, e
	}
	sealed, e := a.Secret.Seal([]byte(key))
	if e != nil {
		return nil, e
	}
	b, e := json.Marshal(sealed)
	if e != nil {
		return nil, e
	}
	if e = a.Store.SetPendingMFA(ctx, u.ID, string(b)); e != nil {
		return nil, mfaStateError(e)
	}
	return struct {
		URI string `json:"otpauth_uri"`
	}{uri}, nil
}
func mfaStateError(e error) error {
	if errors.Is(e, store.ErrAuthConflict) {
		return rpc.Errf("conflict", "MFA state changed")
	}
	if errors.Is(e, store.ErrAuthExpired) {
		return authFailure()
	}
	return e
}
func mfaConfirm(ctx context.Context, a *agent.Agent, v any) (any, error) {
	u, e := a.Store.UserByID(ctx, rpc.CallerFrom(ctx).UserID)
	if e != nil {
		return nil, e
	}
	now := a.Now()
	if a.Secret == nil || u.TOTPPending == nil || u.TOTPPendingAt == nil || *u.TOTPPendingAt > now || now-*u.TOTPPendingAt >= 600000 {
		return nil, authFailure()
	}
	var sealed secret.Sealed
	if e = json.Unmarshal([]byte(*u.TOTPPending), &sealed); e != nil {
		return nil, rpc.Errf("agent_error", "MFA unavailable")
	}
	plain, e := a.Secret.Open(sealed)
	if e != nil {
		return nil, rpc.Errf("agent_error", "MFA unavailable")
	}
	defer clear(plain)
	var step int64
	ok, e := a.MFAFailures.Verify(u.ID, now, a.Cfg.TOTPMaxAttempts, int64(a.Cfg.LoginWindowSec)*1000, func() (bool, error) {
		var valid bool
		step, valid = auth.VerifyTOTP(string(plain), v.(*reauthArgs).TOTP, time.UnixMilli(now))
		return valid, nil
	})
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, authFailure()
	}
	codes := make([]string, 8)
	rows := make([]store.BackupCode, 8)
	for i := range codes {
		var b [16]byte
		if _, e = rand.Read(b[:]); e != nil {
			return nil, e
		}
		codes[i] = hex.EncodeToString(b[:])
		cid, err := id.New()
		if err != nil {
			return nil, err
		}
		phc := auth.Hash(codes[i])
		if phc == "" {
			return nil, rpc.Errf("agent_error", "Password hashing unavailable")
		}
		rows[i] = store.BackupCode{ID: cid, UserID: u.ID, CodePHC: phc}
	}
	if e = a.Store.ConfirmMFA(ctx, u.ID, *u.TOTPPending, step, 600000, rows); e != nil {
		return nil, mfaStateError(e)
	}
	return struct {
		Codes []string `json:"backup_codes"`
	}{codes}, nil
}
func mfaDisable(ctx context.Context, a *agent.Agent, v any) (any, error) {
	u, e := a.Store.UserByID(ctx, rpc.CallerFrom(ctx).UserID)
	if e != nil {
		return nil, e
	}
	if e = verifyMFA(ctx, a.Store, a.Secret, &a.MFAFailures, a.Cfg.TOTPMaxAttempts, int64(a.Cfg.LoginWindowSec)*1000, a.Now(), u, v.(*reauthArgs).TOTP); e != nil {
		return nil, e
	}
	if e = a.Store.DisableMFA(ctx, u.ID, *u.TOTPSecret); e != nil {
		return nil, mfaStateError(e)
	}
	return struct{}{}, nil
}

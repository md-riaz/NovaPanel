package verbs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
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

type loginArgs struct {
	User     string `json:"user"`
	Password string `json:"password"`
	TOTP     string `json:"totp,omitempty"`
}
type reauthArgs struct {
	TOTP string `json:"totp"`
}

func init() {
	rpc.Register(rpc.Verb{Name: "session.start", Public: true, NewArgs: func() any { return &emptyArgs{} }, Run: sessionStart})
	rpc.Register(rpc.Verb{Name: "session.login", Public: true, NewArgs: func() any { return &loginArgs{} }, Run: sessionLogin})
	rpc.Register(rpc.Verb{Name: "session.logout", MinRole: rpc.ReadOnly, NewArgs: func() any { return &emptyArgs{} }, Run: sessionLogout})
	rpc.Register(rpc.Verb{Name: "session.whoami", MinRole: rpc.ReadOnly, ReadOnly: true, NewArgs: func() any { return &emptyArgs{} }, Run: sessionWhoami})
	rpc.Register(rpc.Verb{Name: "session.reauth", MinRole: rpc.ReadOnly, NewArgs: func() any { return &reauthArgs{} }, Run: sessionReauth})
}
func authFailure() error { return rpc.Errf("unauthenticated", "Authentication required") }
func randomSession(now int64) (store.Session, string, error) {
	sid, e := id.New()
	if e != nil {
		return store.Session{}, "", e
	}
	var b [64]byte
	if _, e = rand.Read(b[:]); e != nil {
		return store.Session{}, "", e
	}
	secret := hex.EncodeToString(b[:32])
	csrf := hex.EncodeToString(b[32:])
	digest := sha256.Sum256([]byte(secret))
	return store.Session{ID: sid, TokenHash: hex.EncodeToString(digest[:]), Kind: "anon", CSRFToken: csrf, IdleDeadline: now + 600000, AbsDeadline: now + 43200000}, "nps_" + sid + "_" + secret, nil
}
func sessionStart(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	if a.Store == nil {
		return nil, rpc.Errf("unsupported", "Unavailable in this phase")
	}
	row, token, e := randomSession(a.Now())
	if e != nil {
		return nil, e
	}
	row.AbsDeadline = row.IdleDeadline
	if e = a.Store.CreateSession(ctx, row); e != nil {
		return nil, e
	}
	return struct {
		Session string `json:"session"`
		CSRF    string `json:"csrf_token"`
	}{token, row.CSRFToken}, nil
}

// Fixed policy-cost dummy hash keeps unknown-user attempts on the Argon2 path.
var dummyPHC = auth.Hash("novapanel unknown-user timing dummy")

func sessionLogin(ctx context.Context, a *agent.Agent, v any) (any, error) {
	c := rpc.CallerFrom(ctx)
	if a.Store == nil || c.Kind != "anon" || c.Session == "" {
		return nil, authFailure()
	}
	args := v.(*loginArgs)
	u, e := a.Store.UserByName(ctx, args.User)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	phc := u.PWPHC
	if e != nil {
		phc = dummyPHC
	}
	ok, rehash := auth.Verify(args.Password, phc)
	if !ok || e != nil || u.Disabled {
		return nil, authFailure()
	}
	mfa := u.TOTPSecret != nil
	if mfa {
		if e = verifyMFA(ctx, a.Store, a.Secret, a.Now(), u, args.TOTP); e != nil {
			return nil, e
		}
	}
	fresh := ""
	if rehash {
		fresh = auth.Hash(args.Password)
		if fresh == "" {
			return nil, rpc.Errf("agent_error", "Password hashing unavailable")
		}
	}
	row, token, e := randomSession(a.Now())
	if e != nil {
		return nil, e
	}
	row.Kind = "user"
	row.UserID = &u.ID
	row.Role = &u.Role
	if mfa {
		now := a.Now()
		row.MFAAt = &now
	}
	if e = a.Store.RotateVerifiedSession(ctx, c.Session, row, u, fresh); e != nil {
		if errors.Is(e, store.ErrAuthConflict) || errors.Is(e, store.ErrAuthExpired) || errors.Is(e, sql.ErrNoRows) {
			return nil, authFailure()
		}
		return nil, e
	}
	return struct {
		Session string `json:"session"`
		Role    string `json:"role"`
		CSRF    string `json:"csrf_token"`
		MFA     bool   `json:"mfa"`
	}{token, u.Role, row.CSRFToken, mfa}, nil
}
func sessionLogout(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	if e := a.Store.DeleteSession(ctx, rpc.CallerFrom(ctx).Session); e != nil {
		return nil, e
	}
	return struct{}{}, nil
}
func sessionWhoami(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	c := rpc.CallerFrom(ctx)
	if c.Kind != "user" {
		return nil, authFailure()
	}
	row, e := a.Store.GetSession(ctx, c.Session)
	if e != nil {
		return nil, e
	}
	u, e := a.Store.UserByID(ctx, c.UserID)
	if e != nil {
		return nil, e
	}
	return struct {
		User  string `json:"user"`
		Role  string `json:"role"`
		CSRF  string `json:"csrf_token"`
		MFAAt int64  `json:"mfa_at"`
	}{u.Username, c.Role, row.CSRFToken, c.MFAat}, nil
}
func verifyMFA(ctx context.Context, st *store.Store, sealer *secret.Sealer, now int64, u store.User, proof string) error {
	if u.TOTPSecret == nil || sealer == nil {
		return authFailure()
	}
	var sealed secret.Sealed
	if e := json.Unmarshal([]byte(*u.TOTPSecret), &sealed); e != nil {
		return rpc.Errf("agent_error", "MFA unavailable")
	}
	plain, e := sealer.Open(sealed)
	if e != nil {
		return rpc.Errf("agent_error", "MFA unavailable")
	}
	defer clear(plain)
	if step, ok := auth.VerifyTOTP(string(plain), proof, time.UnixMilli(now)); ok {
		if e = st.ConsumeTOTP(ctx, u.ID, *u.TOTPSecret, step); e == nil {
			return nil
		}
		if errors.Is(e, store.ErrAuthConflict) {
			return authFailure()
		}
		return e
	}
	codes, e := st.ListBackupCodes(ctx, u.ID)
	if e != nil {
		return e
	}
	for _, code := range codes {
		if ok, _ := auth.Verify(proof, code.CodePHC); ok {
			if e = st.ConsumeBackupCode(ctx, u.ID, code.ID, code.CodePHC); e == nil {
				return nil
			}
			if errors.Is(e, store.ErrAuthConflict) {
				return authFailure()
			}
			return e
		}
	}
	return authFailure()
}
func sessionReauth(ctx context.Context, a *agent.Agent, v any) (any, error) {
	c := rpc.CallerFrom(ctx)
	u, e := a.Store.UserByID(ctx, c.UserID)
	if e != nil {
		return nil, e
	}
	if e = verifyMFA(ctx, a.Store, a.Secret, a.Now(), u, v.(*reauthArgs).TOTP); e != nil {
		return nil, e
	}
	old, e := a.Store.GetSession(ctx, c.Session)
	if e != nil {
		return nil, e
	}
	row, token, e := randomSession(a.Now())
	if e != nil {
		return nil, e
	}
	row.Kind = "user"
	row.UserID = &u.ID
	row.Role = &u.Role
	now := a.Now()
	row.MFAAt = &now
	row.AbsDeadline = old.AbsDeadline
	if row.IdleDeadline > row.AbsDeadline {
		row.IdleDeadline = row.AbsDeadline
	}
	if e = a.Store.RotateSession(ctx, c.Session, row); e != nil {
		return nil, e
	}
	return struct {
		Session string `json:"session"`
		CSRF    string `json:"csrf_token"`
		MFAAt   int64  `json:"mfa_at"`
	}{token, row.CSRFToken, now}, nil
}

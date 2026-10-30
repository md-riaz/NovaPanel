package rpc

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"novapanel/internal/agent"
	"novapanel/internal/auth"
	"novapanel/internal/id"
	"strings"
)

type storedSessions struct{ a *agent.Agent }

func (r storedSessions) Resolve(ctx context.Context, token string) (Caller, error) {
	return r.resolve(ctx, token, true)
}
func (r storedSessions) resolve(ctx context.Context, token string, touch bool) (Caller, error) {
	deny := func() (Caller, error) { return Caller{}, Errf("unauthenticated", "Authentication required") }
	parts := strings.Split(token, "_")
	if len(parts) != 3 || !id.Valid(parts[1]) || len(parts[2]) != 64 {
		return deny()
	}
	raw, e := hex.DecodeString(parts[2])
	if e != nil || hex.EncodeToString(raw) != parts[2] {
		return deny()
	}
	if parts[0] == "npat" {
		t, e := r.a.Store.GetAPIToken(ctx, parts[1])
		if e != nil {
			return deny()
		}
		ok, _ := auth.Verify(parts[2], t.SecretPHC)
		if !ok {
			return deny()
		}
		if e = r.a.Store.TouchAPIToken(ctx, t.ID, t.SecretPHC); e != nil {
			return deny()
		}
		return Caller{Kind: "token", Session: t.ID, Scopes: t.Scopes, Automation: t.Automation}, nil
	}
	if parts[0] != "nps" {
		return deny()
	}
	s, e := r.a.Store.GetSession(ctx, parts[1])
	if e != nil {
		return deny()
	}
	digest := sha256.Sum256([]byte(parts[2]))
	h := hex.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(s.TokenHash), []byte(h)) != 1 {
		return deny()
	}
	c := Caller{Kind: s.Kind, Session: s.ID}
	if s.Kind == "user" {
		if s.UserID == nil || s.Role == nil {
			return deny()
		}
		u, e := r.a.Store.UserByID(ctx, *s.UserID)
		if e != nil || u.Disabled || u.Role != *s.Role {
			return deny()
		}
		c.UserID = u.ID
		c.Role = u.Role
	} else if s.Kind != "anon" {
		return deny()
	}
	if s.MFAAt != nil {
		c.MFAat = *s.MFAAt
	}
	if touch {
		if _, e = r.a.Store.TouchSession(ctx, s.ID, h, 600000); e != nil {
			return deny()
		}
	}
	return c, nil
}

package rpc

import (
	"context"
	"novapanel/internal/audit"
	"time"
)

type Caller struct {
	Automation           bool
	Kind                 string
	Session, Actor, Role string
	Scopes               []string
	UserID               string
	MFAat                int64
	Peer                 uint32
}
type callerKey struct{}
type presentedSessionKey struct{}

// PresentedSession returns the transport credential, never an authorization input.
func PresentedSession(ctx context.Context) string {
	v, _ := ctx.Value(presentedSessionKey{}).(string)
	return v
}

func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}
func callerIdentity(c Caller) string {
	if c.Session != "" {
		return c.Session
	}
	if c.Peer == 0 && c.Role == "owner" {
		return "local-root"
	}
	return ""
}
func callerPrincipal(c Caller) audit.Principal {
	if c.Kind == "token" || c.Role == "token" {
		return audit.Principal{Kind: "token", TokenID: c.Session}
	}
	if c.Kind == "user" || c.Kind == "anon" {
		return audit.Principal{Kind: c.Kind, UserID: c.UserID, SessionID: c.Session}
	}
	if callerIdentity(c) == "local-root" {
		return audit.Principal{Kind: "local-root"}
	}
	return audit.Principal{Kind: "unresolved"}
}
func CallerFrom(ctx context.Context) Caller { c, _ := ctx.Value(callerKey{}).(Caller); return c }
func RootCaller(uid uint32) Caller {
	if uid != 0 {
		return Caller{Peer: uid}
	}
	return Caller{Role: "owner", Actor: "root@local", Scopes: []string{"*"}, MFAat: time.Now().UnixMilli(), Peer: 0}
}

// SessionResolver resolves agent-minted identities; unknown tokens fail closed.
type SessionResolver interface {
	Resolve(context.Context, string) (Caller, error)
}
type closedSessions struct{}

func (closedSessions) Resolve(context.Context, string) (Caller, error) {
	return Caller{}, Errf("unauthenticated", "Authentication required")
}

package rpc

import (
	"context"
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
func CallerFrom(ctx context.Context) Caller { c, _ := ctx.Value(callerKey{}).(Caller); return c }
func RootCaller(uid uint32) Caller {
	if uid != 0 {
		return Caller{Peer: uid}
	}
	return Caller{Role: "owner", Actor: "root@local", Scopes: []string{"*"}, MFAat: time.Now().UnixMilli(), Peer: 0}
}

// SessionResolver will resolve agent-minted identities in Phase 2. Unknown tokens fail closed.
type SessionResolver interface {
	Resolve(context.Context, string) (Caller, error)
}
type closedSessions struct{}

func (closedSessions) Resolve(context.Context, string) (Caller, error) {
	return Caller{}, Errf("unauthenticated", "Authentication required")
}

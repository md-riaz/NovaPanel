package verbs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"novapanel/internal/agent"
	"novapanel/internal/auth"
	"novapanel/internal/id"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
)

type tokenCreateArgs struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	Expires    *int64   `json:"expires,omitempty"`
	Automation bool     `json:"automation,omitempty"`
}
type tokenIDArgs struct {
	ID string `json:"token_id"`
}

func init() {
	rpc.Register(rpc.Verb{Name: "tokens.create", MinRole: rpc.Admin, MFA: true, NewArgs: func() any { return &tokenCreateArgs{} }, Run: tokensCreate})
	rpc.Register(rpc.Verb{Name: "tokens.list", MinRole: rpc.Admin, ReadOnly: true, NewArgs: func() any { return &emptyArgs{} }, Run: tokensList})
	rpc.Register(rpc.Verb{Name: "tokens.revoke", MinRole: rpc.Admin, NewArgs: func() any { return &tokenIDArgs{} }, Run: tokensRevoke})
}
func (v *tokenCreateArgs) Validate() error {
	if !identityText(v.Name) || v.Scopes == nil {
		return invalidIdentity()
	}
	seen := map[string]bool{}
	for _, s := range v.Scopes {
		if seen[s] {
			return invalidIdentity()
		}
		seen[s] = true
		switch s {
		case "system:read", "system:write", "sites:read", "sites:write", "databases:read", "databases:write", "dns:read", "dns:write", "backups:read", "backups:write", "deploy":
		case "backups:restore", "firewall:write", "ssh:write", "tokens:write", "system:power":
			if !v.Automation {
				return invalidIdentity()
			}
		default:
			return invalidIdentity()
		}
	}
	return nil
}
func (v *tokenIDArgs) Validate() error {
	if !id.Valid(v.ID) {
		return invalidIdentity()
	}
	return nil
}
func tokensCreate(ctx context.Context, a *agent.Agent, v any) (any, error) {
	c := rpc.CallerFrom(ctx)
	// Root has no users row; token ownership must never be inferred or fabricated.
	if c.Kind != "user" || c.UserID == "" {
		return nil, rpc.Errf("forbidden", "User session required")
	}
	args := v.(*tokenCreateArgs)
	if args.Expires != nil && *args.Expires <= a.Now() {
		return nil, invalidIdentity()
	}
	tid, e := id.New()
	if e != nil {
		return nil, e
	}
	var b [32]byte
	if _, e = rand.Read(b[:]); e != nil {
		return nil, e
	}
	plain := hex.EncodeToString(b[:])
	phc := auth.Hash(plain)
	if phc == "" {
		return nil, rpc.Errf("agent_error", "Password hashing unavailable")
	}
	if e = a.Store.CreateAPIToken(ctx, store.APIToken{ID: tid, Name: args.Name, SecretPHC: phc, Scopes: args.Scopes, Automation: args.Automation, CreatedBy: c.UserID, Expires: args.Expires}); e != nil {
		return nil, identityError(e)
	}
	return struct {
		Token string `json:"token"`
	}{"npat_" + tid + "_" + plain}, nil
}

type tokenView struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	Automation bool     `json:"automation"`
	Expires    *int64   `json:"expires"`
	LastUsed   *int64   `json:"last_used"`
	Revoked    bool     `json:"revoked"`
}

func tokensList(ctx context.Context, a *agent.Agent, _ any) (any, error) {
	rows, e := a.Store.ListAPITokens(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]tokenView, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenView{r.ID, r.Name, r.Scopes, r.Automation, r.Expires, r.LastUsed, r.Revoked})
	}
	return struct {
		Tokens []tokenView `json:"tokens"`
	}{out}, nil
}
func tokensRevoke(ctx context.Context, a *agent.Agent, v any) (any, error) {
	return struct{}{}, identityError(a.Store.RevokeAPIToken(ctx, v.(*tokenIDArgs).ID))
}

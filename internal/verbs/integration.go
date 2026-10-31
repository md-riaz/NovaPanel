package verbs

import (
	"context"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/dns"
	"novapanel/internal/rpc"
	"novapanel/internal/store"
	"novapanel/internal/validate"
	"strings"
)

type integrationSetArgs struct {
	Kind  string   `json:"kind"`
	Token string   `json:"token"`
	Zones []string `json:"zones"`
}

func (v *integrationSetArgs) Validate() error {
	token, e := dns.NewIntegrationToken([]byte(v.Token))
	if e != nil {
		return rpc.Errf("invalid_argument", "Invalid integration")
	}
	token.Destroy()
	if v.Kind != "cloudflare" || len(v.Zones) == 0 || len(v.Zones) > 100 {
		return rpc.Errf("invalid_argument", "Invalid integration")
	}
	seen := map[string]bool{}
	for _, z := range v.Zones {
		if z != strings.ToLower(z) || (&validate.V{}).Hostname(z) != nil || seen[z] {
			return rpc.Errf("invalid_argument", "Invalid integration")
		}
		seen[z] = true
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "integration.set", MinRole: rpc.Admin, MFA: true, MaxDeadlineMs: 30000, NewArgs: func() any { return &integrationSetArgs{} }, Run: integrationSet})
}
func integrationSet(ctx context.Context, a *agent.Agent, arg any) (any, error) {
	if a.IntegrationProbe == nil || a.Store == nil || a.Secret == nil {
		return nil, rpc.Errf("unsupported", "Integration admission unavailable")
	}
	v := arg.(*integrationSetArgs)
	token, e := dns.NewIntegrationToken([]byte(v.Token))
	v.Token = ""
	if e != nil {
		return nil, rpc.Errf("invalid_argument", "Invalid integration")
	}
	defer token.Destroy()
	// Only a trusted probe may attest exact scope. No default live implementation exists.
	spec, e := a.IntegrationProbe.Probe(ctx, token, append([]string(nil), v.Zones...))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if e != nil {
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			return nil, e
		}
		return nil, rpc.Errf("forbidden", "Integration scope could not be verified")
	}
	if spec.Validate() != nil || spec.Kind != v.Kind || len(spec.Zones) != len(v.Zones) {
		return nil, rpc.Errf("forbidden", "Integration scope could not be verified")
	}
	names := map[string]bool{}
	for _, z := range spec.Zones {
		names[z.Zone] = true
	}
	for _, z := range v.Zones {
		if !names[z] {
			return nil, rpc.Errf("forbidden", "Integration scope could not be verified")
		}
	}
	plain := token.ExecutionBytes()
	defer clear(plain)
	sealed, e := a.Secret.Seal(plain)
	if e != nil {
		return nil, rpc.Errf("agent_error", "Integration sealing failed")
	}
	caller := rpc.CallerFrom(ctx)
	authority := store.IntegrationAuthority{UserID: caller.UserID}
	if caller.Peer == 0 && caller.Role == "owner" && caller.Kind == "" && caller.Session == "" {
		authority = store.IntegrationAuthority{LocalRoot: true}
	}
	result, e := a.Store.CreateDNSIntegration(ctx, authority, spec, store.IntegrationSealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
	if e != nil {
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			return nil, e
		}
		return nil, rpc.Errf("forbidden", "Integration admission refused")
	}
	return result, nil
}

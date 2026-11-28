package verbs

import (
	"context"
	"errors"
	"novapanel/internal/agent"
	"novapanel/internal/id"
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
	if v.Kind != "cloudflare" || len(v.Token) == 0 || len(v.Token) > 4096 || strings.ContainsAny(v.Token, "\r\n") || len(v.Zones) == 0 || len(v.Zones) > 100 {
		return rpc.Errf("invalid_argument", "Invalid integration")
	}
	seen := map[string]bool{}
	for _, z := range v.Zones {
		if z != strings.ToLower(z) || seen[z] || (&validate.V{}).Hostname(z) != nil {
			return rpc.Errf("invalid_argument", "Invalid zone")
		}
		seen[z] = true
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "integration.set", MinRole: rpc.Admin, MFA: true, NewArgs: func() any { return &integrationSetArgs{} }, Run: integrationSet})
}
func integrationSet(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*integrationSetArgs)
	if a.Secret == nil || a.Store == nil || a.ProbeDNS == nil {
		return nil, rpc.Errf("agent_error", "Integration unavailable")
	}
	sealed, err := a.Secret.Seal([]byte(v.Token))
	if err != nil {
		return nil, rpc.Errf("agent_error", "Integration unavailable")
	}
	zoneIDs, err := a.ProbeDNS(ctx, v.Token, v.Zones)
	if err != nil {
		return nil, rpc.Errf("precondition_failed", "Cloudflare zone access check failed")
	}
	integrationID, err := id.New()
	if err != nil {
		return nil, rpc.Errf("agent_error", "Integration unavailable")
	}
	if len(zoneIDs) != len(v.Zones) {
		return nil, rpc.Errf("precondition_failed", "Cloudflare zone access check failed")
	}
	for _, zone := range v.Zones {
		if zoneIDs[zone] == "" {
			return nil, rpc.Errf("precondition_failed", "Cloudflare zone access check failed")
		}
	}
	integrationID, err = a.Store.SetCloudflareIntegration(ctx, integrationID, store.ACMESealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}, zoneIDs)
	if errors.Is(err, store.ErrZoneManaged) {
		return nil, rpc.Errf("precondition_failed", "zone managed by another integration")
	}
	if err != nil {
		return nil, rpc.Errf("agent_error", "Integration unavailable")
	}
	return struct {
		IntegrationID string `json:"integration_id"`
	}{integrationID}, nil
}

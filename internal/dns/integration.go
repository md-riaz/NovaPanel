package dns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"novapanel/internal/validate"
	"regexp"
	"strings"
)

var ErrIntegration = errors.New("invalid DNS integration")
var providerZoneID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// IntegrationProbe must prove Zone:Read, DNS:Edit and Cache Purge on exactly
// the requested zones without writes. Nil production configuration fails closed.
type IntegrationProbe interface {
	Probe(context.Context, *IntegrationToken, []string) (IntegrationSpec, error)
}

type ZoneBinding struct {
	Zone           string `json:"zone"`
	ProviderZoneID string `json:"provider_zone_id"`
}
type IntegrationSpec struct {
	Kind  string        `json:"kind"`
	Zones []ZoneBinding `json:"zones"`
}

func (v IntegrationSpec) Validate() error {
	if v.Kind != "cloudflare" || len(v.Zones) == 0 || len(v.Zones) > 100 {
		return ErrIntegration
	}
	names, ids := map[string]bool{}, map[string]bool{}
	for _, z := range v.Zones {
		if z.Zone != strings.ToLower(z.Zone) || (&validate.V{}).Hostname(z.Zone) != nil || !providerZoneID.MatchString(z.ProviderZoneID) || names[z.Zone] || ids[z.ProviderZoneID] {
			return ErrIntegration
		}
		names[z.Zone], ids[z.ProviderZoneID] = true, true
	}
	return nil
}

// IntegrationToken is execution-only, never an RPC result or durable job argument.
type IntegrationToken struct{ value []byte }

func NewIntegrationToken(v []byte) (*IntegrationToken, error) {
	if len(v) == 0 || len(v) > 4096 {
		return nil, ErrIntegration
	}
	for _, b := range v {
		if b < 33 || b > 126 {
			return nil, ErrIntegration
		}
	}
	return &IntegrationToken{value: append([]byte(nil), v...)}, nil
}
func (*IntegrationToken) Format(s fmt.State, _ rune)   { _, _ = io.WriteString(s, "[REDACTED]") }
func (*IntegrationToken) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }

// ExecutionBytes returns a copy owned by the caller, which must clear it after use.
func (v *IntegrationToken) ExecutionBytes() []byte { return append([]byte(nil), v.value...) }
func (v *IntegrationToken) Destroy()               { clear(v.value); v.value = nil }

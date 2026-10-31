package dns

import (
	"context"
	"net/netip"
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type Verification struct {
	PointsHere bool   `json:"points_here"`
	Method     string `json:"method"`
}

// Provider is an execution adapter, not durable job recovery or credential storage.
type Provider interface {
	List(context.Context, string) ([]Record, error)
	Create(context.Context, string, Record) (Record, error)
	Update(context.Context, string, Record) error
	Delete(context.Context, string, string) error
	Verify(context.Context, string, Resolver, []netip.Addr) (Verification, error)
	Purge(context.Context, string, []string) error
}

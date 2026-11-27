package dns

import "context"

type Record struct {
	ID, Type, Name, Value string
	TTL, Priority         int
	Proxied               bool
}

type Provider interface {
	List(context.Context, string) ([]Record, error)
	Create(context.Context, string, Record) error
	Update(context.Context, string, Record) error
	Delete(context.Context, string, Record) error
	Verify(context.Context, string) (bool, error)
	Purge(context.Context, string, []string) error
	PresentTXT(context.Context, string, string) error
	CleanupTXT(context.Context, string, string) error
}

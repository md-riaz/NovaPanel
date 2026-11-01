package acme

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"golang.org/x/net/publicsuffix"
	"novapanel/internal/store"
)

// Policy is the durable callback coordinator; callers serialize account use across clients.
type Policy struct {
	Store *store.Store
	Now   func() int64
}

func (p Policy) buckets(o Order, failed bool) ([]store.ACMEBucket, error) {
	if p.Store == nil || p.Now == nil {
		return nil, store.ErrACMEPolicy
	}
	if _, e := accountName(o.Environment); e != nil {
		return nil, e
	}
	if e := checkDomains(o.Domains); e != nil {
		return nil, e
	}
	names := append([]string(nil), o.Domains...)
	sort.Strings(names)
	registered := map[string]bool{}
	for _, name := range names {
		domain, e := publicsuffix.EffectiveTLDPlusOne(name)
		if e != nil {
			return nil, store.ErrACMEPolicy
		}
		registered[domain] = true
	}
	prefix := string(o.Environment) + ":"
	out := []store.ACMEBucket{}
	for domain := range registered {
		if !failed {
			out = append(out, store.ACMEBucket{Key: prefix + "domain:" + domain, Limit: 50, Window: 604800000})
		}
		out = append(out, store.ACMEBucket{Key: prefix + "auth:" + domain, Limit: 5, Window: 3600000, CheckOnly: !failed})
	}
	if !failed {
		hash := sha256.Sum256([]byte(strings.Join(names, "\x00")))
		out = append(out, store.ACMEBucket{Key: prefix + "set:" + hex.EncodeToString(hash[:]), Limit: 5, Window: 604800000})
	}
	return out, nil
}
func (p Policy) BeforeOrder(ctx context.Context, o Order) error {
	b, e := p.buckets(o, false)
	if e != nil {
		return e
	}
	return p.Store.ReserveACME(ctx, b, p.Now())
}

// FailedOrder charges ambiguous CA failures conservatively, never refunds issuance.
func (p Policy) FailedOrder(ctx context.Context, o Order) error {
	b, e := p.buckets(o, true)
	if e != nil {
		return e
	}
	return p.Store.ReserveACME(ctx, b, p.Now())
}

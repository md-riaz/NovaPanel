package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode"

	"novapanel/internal/agent"
	"novapanel/internal/dns"
	"novapanel/internal/id"
	"novapanel/internal/jobs"
	"novapanel/internal/rpc"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"novapanel/internal/validate"
)

type dnsCreateArgs struct {
	Zone     string `json:"zone"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	TTL      *int   `json:"ttl,omitempty"`
	Priority *int   `json:"priority,omitempty"`
	Proxied  *bool  `json:"proxied,omitempty"`
	RecordID string `json:"record_id,omitempty"`
}
type dnsUpdateArgs struct {
	Zone     string  `json:"zone"`
	RecordID string  `json:"record_id"`
	Type     *string `json:"type,omitempty"`
	Name     *string `json:"name,omitempty"`
	Value    *string `json:"value,omitempty"`
	TTL      *int    `json:"ttl,omitempty"`
	Priority *int    `json:"priority,omitempty"`
	Proxied  *bool   `json:"proxied,omitempty"`
}
type dnsIDArgs struct {
	Zone     string `json:"zone"`
	RecordID string `json:"record_id"`
}
type dnsZoneArgs struct {
	Zone string `json:"zone"`
}

func (v *dnsZoneArgs) Validate() error {
	if v.Zone != strings.ToLower(v.Zone) || (&validate.V{}).Hostname(v.Zone) != nil {
		return rpc.Errf("invalid_argument", "Invalid DNS zone")
	}
	return nil
}
func (v *dnsIDArgs) Validate() error {
	if (&dnsZoneArgs{v.Zone}).Validate() != nil || !id.Valid(v.RecordID) {
		return rpc.Errf("invalid_argument", "Invalid DNS record identity")
	}
	return nil
}
func (v *dnsUpdateArgs) Validate() error { return (&dnsIDArgs{v.Zone, v.RecordID}).Validate() }
func (v *dnsCreateArgs) Validate() error {
	if (&dnsZoneArgs{v.Zone}).Validate() != nil {
		return rpc.Errf("invalid_argument", "Invalid DNS zone")
	}
	ttl := 1
	if v.TTL != nil {
		ttl = *v.TTL
	}
	priority := 0
	if v.Priority != nil {
		priority = *v.Priority
	}
	proxied := false
	if v.Proxied != nil {
		proxied = *v.Proxied
	}
	return validateDNSRecord(v.Zone, store.DNSRecord{Type: v.Type, Name: v.Name, Value: v.Value, TTL: ttl, Priority: priority, Proxied: proxied}, v.Priority != nil)
}
func validateDNSRecord(zone string, r store.DNSRecord, hasPriority bool) error {
	invalid := func() error { return rpc.Errf("invalid_argument", "Invalid DNS record") }
	if r.Name != strings.ToLower(r.Name) || !(r.Name == zone || strings.HasSuffix(r.Name, "."+zone)) || len(r.Name) > 253 {
		return invalid()
	}
	// DNS owner names can use service labels such as _dmarc and _domainkey.
	labels := strings.Split(r.Name, ".")
	for i, part := range labels {
		if len(part) > 63 {
			return invalid()
		}
		labels[i] = strings.TrimPrefix(part, "_")
	}
	host := strings.Join(labels, ".")
	if (&validate.V{}).Hostname(host) != nil || (r.TTL != 1 && (r.TTL < 60 || r.TTL > 86400)) || r.Priority < 0 || r.Priority > 65535 {
		return invalid()
	}
	web := r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME"
	if r.Proxied && !web {
		return invalid()
	}
	if r.Type != "MX" && (hasPriority || r.Priority != 0) {
		return invalid()
	}
	switch r.Type {
	case "A", "AAAA":
		ip, err := netip.ParseAddr(r.Value)
		if err != nil || (r.Type == "A" && !ip.Is4()) || (r.Type == "AAAA" && (!ip.Is6() || ip.Is4In6() || ip.Zone() != "")) {
			return invalid()
		}
	case "CNAME", "MX":
		if (&validate.V{}).Hostname(r.Value) != nil {
			return invalid()
		}
	case "TXT":
		if len(r.Value) == 0 || len(r.Value) > 4096 || strings.IndexFunc(r.Value, unicode.IsControl) >= 0 {
			return invalid()
		}
	default:
		return invalid()
	}
	return nil
}
func init() {
	rpc.Register(rpc.Verb{Name: "dns.record.create", MinRole: rpc.Admin, Scope: "dns:write", Slow: true, Effectful: true, NewArgs: func() any { return &dnsCreateArgs{} }, ObjectKey: func(v any) string { return "zone:" + v.(*dnsCreateArgs).Zone }, Run: dnsCreate, Exec: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (dnsState{a}).execute(ctx, j) }})
	rpc.Register(rpc.Verb{Name: "dns.record.update", MinRole: rpc.Admin, Scope: "dns:write", Slow: true, Effectful: true, NewArgs: func() any { return &dnsUpdateArgs{} }, ObjectKey: func(v any) string { return "zone:" + v.(*dnsUpdateArgs).Zone }, Run: dnsUpdate, Exec: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (dnsState{a}).execute(ctx, j) }})
	rpc.Register(rpc.Verb{Name: "dns.record.delete", MinRole: rpc.Admin, Scope: "dns:write", Slow: true, Effectful: true, NewArgs: func() any { return &dnsIDArgs{} }, ObjectKey: func(v any) string { return "zone:" + v.(*dnsIDArgs).Zone }, Run: dnsDelete, Exec: func(ctx context.Context, a *agent.Agent, j *jobs.Job) error { return (dnsState{a}).execute(ctx, j) }})
	rpc.Register(rpc.Verb{Name: "dns.record.list", MinRole: rpc.ReadOnly, ReadOnly: true, Scope: "dns:read", NewArgs: func() any { return &dnsZoneArgs{} }, Run: dnsList})
}

type dnsState struct{ a *agent.Agent }

func (d dnsState) zone(ctx context.Context, name string) (store.DNSZone, error) {
	a := d.a
	if a.Store == nil {
		return store.DNSZone{}, rpc.Errf("agent_error", "DNS unavailable")
	}
	z, err := a.Store.DNSZoneByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return z, rpc.Errf("not_found", "DNS zone not found")
	}
	if errors.Is(err, store.ErrDNSOwnership) {
		return z, rpc.Errf("conflict", "DNS zone is ambiguous")
	}
	if err != nil {
		return z, rpc.Errf("agent_error", "DNS unavailable")
	}
	return z, nil
}
func dnsCreate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := *args.(*dnsCreateArgs)
	if v.RecordID != "" {
		return nil, rpc.Errf("invalid_argument", "Record ID is agent assigned")
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if _, err := (dnsState{a}).zone(ctx, v.Zone); err != nil {
		return nil, err
	}
	var err error
	v.RecordID, err = id.New()
	if err != nil {
		return nil, rpc.Errf("agent_error", "DNS unavailable")
	}
	return a.Jobs.Enqueue(ctx, "dns.record.create", &v, "zone:"+v.Zone)
}
func dnsUpdate(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*dnsUpdateArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	z, err := (dnsState{a}).zone(ctx, v.Zone)
	if err != nil {
		return nil, err
	}
	r, err := a.Store.DNSRecord(ctx, v.RecordID)
	if err != nil {
		return nil, dnsError(err)
	}
	if r.ZoneID != z.ID || r.Phase != "created" || !r.Reconciled || r.ConflictError.Valid {
		return nil, rpc.Errf("precondition_failed", "DNS record unavailable")
	}
	mergeDNSUpdate(&r, v)
	if err := validateDNSRecord(v.Zone, r, v.Priority != nil && r.Type != "MX"); err != nil {
		return nil, err
	}
	return a.Jobs.Enqueue(ctx, "dns.record.update", v, "zone:"+v.Zone)
}
func dnsDelete(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*dnsIDArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	z, err := (dnsState{a}).zone(ctx, v.Zone)
	if err != nil {
		return nil, err
	}
	r, err := a.Store.DNSRecord(ctx, v.RecordID)
	if err != nil {
		return nil, dnsError(err)
	}
	if r.ZoneID != z.ID {
		return nil, rpc.Errf("not_found", "DNS record not found")
	}
	return a.Jobs.Enqueue(ctx, "dns.record.delete", v, "zone:"+v.Zone)
}
func dnsError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return rpc.Errf("not_found", "DNS record not found")
	}
	if errors.Is(err, store.ErrDNSOwnership) {
		return rpc.Errf("conflict", "DNS ownership conflict")
	}
	return rpc.Errf("agent_error", "DNS operation unavailable")
}
func mergeDNSUpdate(r *store.DNSRecord, v *dnsUpdateArgs) {
	if v.Type != nil {
		r.Type = *v.Type
	}
	if v.Name != nil {
		r.Name = *v.Name
	}
	if v.Value != nil {
		r.Value = *v.Value
	}
	if v.TTL != nil {
		r.TTL = *v.TTL
	}
	if v.Priority != nil {
		r.Priority = *v.Priority
	}
	if v.Proxied != nil {
		r.Proxied = *v.Proxied
	}
}

type dnsListRecord struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Name          string `json:"name"`
	Value         string `json:"value"`
	TTL           int    `json:"ttl"`
	Proxied       bool   `json:"proxied"`
	ConflictError string `json:"conflict_error,omitempty"`
}

func dnsList(ctx context.Context, a *agent.Agent, args any) (any, error) {
	v := args.(*dnsZoneArgs)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	z, err := (dnsState{a}).zone(ctx, v.Zone)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.DNSRecordsInZone(ctx, z.ID)
	if err != nil {
		return nil, dnsError(err)
	}
	out := make([]dnsListRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, dnsListRecord{r.ObjID, r.Type, r.Name, r.Value, r.TTL, r.Proxied, r.ConflictError.String})
	}
	return struct {
		Records []dnsListRecord `json:"records"`
	}{out}, nil
}

var dnsLocks sync.Map

func dnsLock(ctx context.Context, zone string) (func(), error) {
	candidate := make(chan struct{}, 1)
	candidate <- struct{}{}
	raw, _ := dnsLocks.LoadOrStore(zone, candidate)
	lock := raw.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lock:
		return func() { lock <- struct{}{} }, nil
	}
}
func (d dnsState) provider(ctx context.Context, z store.DNSZone) (dns.Provider, error) {
	a := d.a
	if a.Secret == nil || a.DNSProvider == nil {
		return nil, rpc.Errf("agent_error", "DNS unavailable")
	}
	sealed, err := a.Store.DNSCredential(ctx, z.IntegrationID)
	if err != nil {
		return nil, rpc.Errf("agent_error", "DNS unavailable")
	}
	token, err := a.Secret.Open(secret.Sealed{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
	if err != nil {
		return nil, rpc.Errf("agent_error", "DNS unavailable")
	}
	defer clear(token)
	return a.DNSProvider(string(token), map[string]string{z.Zone: z.ProviderZoneID}), nil
}
func (d dnsState) execute(ctx context.Context, j *jobs.Job) error {
	a := d.a
	var zone, recordID string
	switch j.Verb {
	case "dns.record.create":
		var v dnsCreateArgs
		if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil || !id.Valid(v.RecordID) {
			return rpc.Errf("invalid_argument", "Invalid DNS job")
		}
		zone, recordID = v.Zone, v.RecordID
	case "dns.record.update":
		var v dnsUpdateArgs
		if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil {
			return rpc.Errf("invalid_argument", "Invalid DNS job")
		}
		zone, recordID = v.Zone, v.RecordID
	case "dns.record.delete":
		var v dnsIDArgs
		if json.Unmarshal(j.Args, &v) != nil || v.Validate() != nil {
			return rpc.Errf("invalid_argument", "Invalid DNS job")
		}
		zone, recordID = v.Zone, v.RecordID
	default:
		return rpc.Errf("invalid_argument", "Invalid DNS job")
	}
	if j.ObjectKey != "zone:"+zone {
		return rpc.Errf("precondition_failed", "DNS job binding changed")
	}
	unlock, err := dnsLock(ctx, zone)
	if err != nil {
		return err
	}
	defer unlock()
	z, err := (dnsState{a}).zone(ctx, zone)
	if err != nil {
		return err
	}
	r, err := a.Store.DNSRecord(ctx, recordID)
	if j.Verb == "dns.record.create" && errors.Is(err, sql.ErrNoRows) {
		var v dnsCreateArgs
		_ = json.Unmarshal(j.Args, &v)
		ttl := 1
		if v.TTL != nil {
			ttl = *v.TTL
		}
		priority := 0
		if v.Priority != nil {
			priority = *v.Priority
		}
		proxied := false
		if v.Proxied != nil {
			proxied = *v.Proxied
		}
		r = store.DNSRecord{ObjID: recordID, ZoneID: z.ID, Type: v.Type, Name: v.Name, Value: v.Value, TTL: ttl, Priority: priority, Proxied: proxied}
		if err = a.Store.ReserveDNSRecord(ctx, r); err != nil {
			return dnsError(err)
		}
		r, err = a.Store.DNSRecord(ctx, recordID)
	}
	if errors.Is(err, sql.ErrNoRows) && j.Verb == "dns.record.delete" {
		j.Result = json.RawMessage(`{}`)
		return nil
	}
	if err != nil {
		return dnsError(err)
	}
	if r.ZoneID != z.ID || r.ConflictError.Valid {
		return rpc.Errf("precondition_failed", "DNS record unavailable")
	}
	if j.Verb == "dns.record.update" {
		var v dnsUpdateArgs
		_ = json.Unmarshal(j.Args, &v)
		if r.Phase != "created" || !r.Reconciled {
			return rpc.Errf("precondition_failed", "DNS record unavailable")
		}
		mergeDNSUpdate(&r, &v)
		if err = validateDNSRecord(zone, r, v.Priority != nil && r.Type != "MX"); err != nil {
			return err
		}
		if err = a.Store.BeginDNSUpdate(ctx, r); err != nil {
			return dnsError(err)
		}
		r, err = a.Store.DNSRecord(ctx, recordID)
		if err != nil {
			return dnsError(err)
		}
	} else if j.Verb == "dns.record.delete" {
		if err = a.Store.MarkDNSRecordDeleting(ctx, recordID); err != nil {
			return dnsError(err)
		}
		r, err = a.Store.DNSRecord(ctx, recordID)
		if err != nil {
			return dnsError(err)
		}
	}
	p, err := (dnsState{a}).provider(ctx, z)
	if err != nil {
		return rpc.Errf("interrupted", "DNS reconciliation required")
	}
	if err = a.Store.ReconcileDNSRecord(ctx, p, zone, r); err != nil {
		if errors.Is(err, store.ErrDNSOwnership) {
			return dnsError(err)
		}
		return rpc.Errf("interrupted", "DNS reconciliation required")
	}
	if j.Verb == "dns.record.create" {
		j.Result, _ = json.Marshal(map[string]string{"record_id": recordID})
	} else {
		j.Result = json.RawMessage(`{}`)
	}
	return nil
}

const dnsSweepInterval = time.Minute

func reconcileDNS(ctx context.Context, a *agent.Agent) error {
	rows, err := a.Store.DNSPendingRecords(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, pending := range rows {
		z, err := a.Store.DNSZoneByID(ctx, pending.ZoneID)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		unlock, err := dnsLock(ctx, z.Zone)
		if err != nil {
			return err
		}
		func() {
			defer unlock()
			r, e := a.Store.DNSRecord(ctx, pending.ObjID)
			if errors.Is(e, sql.ErrNoRows) {
				return
			}
			if e != nil {
				result = errors.Join(result, e)
				return
			}
			if r.Reconciled || r.ConflictError.Valid {
				return
			}
			current, e := a.Store.DNSZoneByID(ctx, r.ZoneID)
			if e != nil {
				result = errors.Join(result, e)
				return
			}
			attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			p, e := (dnsState{a}).provider(attempt, current)
			if e == nil {
				e = a.Store.ReconcileDNSRecord(attempt, p, current.Zone, r)
			}
			if e != nil {
				result = errors.Join(result, dnsError(e))
			}
		}()
	}
	return result
}

// StartDNSReconcile attempts outstanding DNS work and backs off on transient failure.
func StartDNSReconcile(ctx context.Context, a *agent.Agent, failed func(error, time.Duration)) (stop func()) {
	if err := reconcileDNS(ctx, a); err == nil {
		return func() {}
	} else if failed != nil {
		failed(err, time.Second)
	}
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		delay := time.Second
		for {
			t := time.NewTimer(delay)
			select {
			case <-c.Done():
				t.Stop()
				return
			case <-t.C:
			}
			if err := reconcileDNS(c, a); err == nil {
				return
			} else if c.Err() == nil && failed != nil {
				failed(err, delay)
			}
			if delay < dnsSweepInterval {
				delay *= 2
				if delay > dnsSweepInterval {
					delay = dnsSweepInterval
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}

// StartDNSSweep retries outstanding transient DNS work.
func StartDNSSweep(ctx context.Context, a *agent.Agent, failed func(error, time.Duration)) (stop func()) {
	c, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(dnsSweepInterval)
		defer t.Stop()
		for {
			select {
			case <-c.Done():
				return
			case <-t.C:
				if err := reconcileDNS(c, a); err != nil && c.Err() == nil && failed != nil {
					failed(err, dnsSweepInterval)
				}
			}
		}
	}()
	return func() { cancel(); <-done }
}

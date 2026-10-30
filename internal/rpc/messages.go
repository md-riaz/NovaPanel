package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"novapanel/internal/agent"
	"novapanel/internal/jobs"
	"novapanel/internal/rpcerr"
	"slices"
	"time"
)

type Role uint8

const (
	ReadOnly Role = iota
	Admin
	Owner
)

type CodedError = rpcerr.CodedError

var Errf = rpcerr.Errf

type Verb struct {
	Name                 string
	Public               bool
	MinRole              Role
	ReadOnly             bool
	Scope                string
	Slow, Effectful, MFA bool
	Module               string
	Internal             bool
	MaxDeadlineMs        int
	NewArgs              func() any
	ObjectKey            func(any) string
	Run                  func(context.Context, *agent.Agent, any) (any, error)
	Exec                 func(context.Context, *agent.Agent, *jobs.Job) error
}

var Registry = map[string]Verb{}

func Register(v Verb) {
	if v.Name == "" || v.NewArgs == nil || v.Run == nil {
		panic("incomplete verb registration")
	}
	if v.Slow != (v.Exec != nil) || (v.Effectful && !v.Slow) {
		panic("inconsistent slow/effectful handler registration")
	}
	if _, ok := Registry[v.Name]; ok {
		panic("duplicate verb: " + v.Name)
	}
	Registry[v.Name] = v
}

type AuditEvent struct{ Actor, Role, Verb, ObjectKey, Outcome, Code string }
type AuditSink interface{ Record(AuditEvent) }
type AuditFunc func(AuditEvent)

func (f AuditFunc) Record(e AuditEvent) { f(e) }

type Server struct {
	Agent             *agent.Agent
	WebUID            uint32
	Registry          map[string]Verb
	Sessions          SessionResolver
	Audit             AuditSink
	DefaultDeadlineMs int
}

func NewServer(a *agent.Agent) *Server {
	return &Server{Agent: a, WebUID: ^uint32(0), Registry: Registry, Sessions: closedSessions{}, Audit: AuditFunc(func(AuditEvent) {}), DefaultDeadlineMs: 120000}
}
func (s *Server) Dispatch(ctx context.Context, req Request) (reply Reply) {
	caller := CallerFrom(ctx)
	event := AuditEvent{Actor: req.Actor, Role: caller.Role, Verb: req.Verb}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("rpc: dispatch panic: %v", r)
			reply = Err("agent_error", "Internal agent error")
		}
		reply.ID = req.ID
		event.Code = reply.Code
		event.Outcome = "ok"
		if !reply.OK {
			event.Outcome = "refused"
			if reply.Code == "agent_error" {
				event.Outcome = "error"
			}
		}
		if s.Audit != nil {
			s.Audit.Record(event)
		}
	}()
	v, ok := s.Registry[req.Verb]
	if !ok {
		return Err("unknown_verb", "Unknown verb")
	}
	// Phase 0 has no modules, queue or durable audit prerequisites. Never
	// invoke a future handler merely because it was added to the registry.
	if v.Module != "" || v.Slow || v.Effectful {
		return Err("unsupported", "Unavailable in this phase")
	}
	if !object(req.Args) {
		return Err("bad_request", "Object args required")
	}
	if caller.Role == "" && caller.Kind == "" && req.Session != "" {
		resolver := s.Sessions
		if resolver == nil {
			resolver = closedSessions{}
		}
		resolved, err := resolver.Resolve(ctx, req.Session)
		if err != nil {
			return Err("unauthenticated", "Authentication required")
		}
		resolved.Peer = caller.Peer
		caller = resolved
	}
	event.Role = caller.Role
	if v.Internal && (caller.Peer != s.WebUID || caller.Peer == 0) {
		return Err("forbidden", "Forbidden")
	}
	if caller.Kind == "token" || caller.Role == "token" {
		if v.Internal || v.Scope == "" || (!slices.Contains(caller.Scopes, v.Scope) && !slices.Contains(caller.Scopes, "*")) {
			return Err("forbidden", "Forbidden")
		}
	} else if !v.Public {
		if caller.Role == "" {
			return Err("unauthenticated", "Authentication required")
		}
		rank, known := map[string]Role{"read_only": ReadOnly, "admin": Admin, "owner": Owner}[caller.Role]
		if !known || rank < v.MinRole || (rank == ReadOnly && !v.ReadOnly) {
			return Err("forbidden", "Forbidden")
		}
	}
	if v.MFA && (caller.MFAat <= 0 || s.Agent.Now()-caller.MFAat > 300000) {
		r := Err("forbidden", "Reauthentication required")
		r.Data = map[string]bool{"reauth_required": true}
		return r
	}
	caller.Actor = req.Actor
	if caller.Actor == "" && caller.Role == "owner" && caller.Peer == 0 {
		caller.Actor = "root@local"
	}
	ctx = WithCaller(ctx, caller)
	args := v.NewArgs()
	d := json.NewDecoder(bytes.NewReader(req.Args))
	d.DisallowUnknownFields()
	if err := d.Decode(args); err != nil {
		return Err("bad_request", "Invalid arguments")
	}
	if validator, ok := args.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			return errorReply(err)
		}
	}
	if v.ObjectKey != nil {
		event.ObjectKey = v.ObjectKey(args)
	}
	max := v.MaxDeadlineMs
	if max <= 0 {
		max = s.DefaultDeadlineMs
	}
	if max <= 0 {
		max = 120000
	}
	ms := req.DeadlineMs
	if ms <= 0 || ms > max {
		ms = max
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	defer cancel()
	data, err := v.Run(ctx, s.Agent, args)
	if ctx.Err() != nil {
		return Err("deadline_exceeded", "Deadline exceeded")
	}
	if err != nil {
		return errorReply(err)
	}
	return OK(data)
}
func errorReply(err error) Reply {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Err("deadline_exceeded", "Deadline exceeded")
	}
	var ce *CodedError
	if errors.As(err, &ce) && validCode(ce.Code) {
		return Err(ce.Code, ce.Msg)
	}
	log.Printf("rpc: handler error: %v", err)
	return Err("agent_error", "Internal agent error")
}
func validCode(code string) bool {
	switch code {
	case "unknown_verb", "bad_request", "invalid_argument", "unauthenticated", "forbidden", "not_found", "conflict", "precondition_failed", "locked", "deadline_exceeded", "agent_error", "interrupted", "unsupported":
		return true
	}
	return false
}

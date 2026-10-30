package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"novapanel/internal/audit"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"sync"
	"time"
)

type Sealer interface {
	Seal([]byte) (secret.Sealed, error)
	Open(secret.Sealed) ([]byte, error)
}
type Auditor interface {
	Record(audit.Event) error
	Healthy() error
}
type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

type Queue struct {
	store   Persistence
	sealer  Sealer
	audit   Auditor
	clock   Clock
	mu      sync.Mutex
	runner  func(context.Context, *Job) error
	running bool
	startup func(context.Context) (*Job, error)
	resume  func(context.Context, *Job) error
	wake    chan struct{}
	locks   keyedLock
}

func NewQueue(s Persistence, sealer Sealer, log Auditor) *Queue {
	return &Queue{store: s, sealer: sealer, audit: log, clock: realClock{}, wake: make(chan struct{}, 1)}
}
func (q *Queue) SetClock(c Clock) { q.clock = c }
func (q *Queue) SetRunner(run func(context.Context, *Job) error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.runner = run
}
func (q *Queue) record(j Job, outcome, code, stage string) error {
	if q.audit == nil {
		return errors.New("jobs: audit not configured")
	}
	if e := q.audit.Healthy(); e != nil {
		return e
	}
	return q.audit.Record(audit.Event{Actor: j.Actor, Role: j.Role, Verb: j.Verb, ObjectKey: j.ObjectKey, Outcome: outcome, Code: code, Detail: map[string]any{"job_id": j.ID, "stage": stage}})
}
func (q *Queue) Enqueue(ctx context.Context, verb string, args any, objectKey string) (EnqueueResult, error) {
	return q.enqueue(ctx, verb, args, objectKey, false)
}
func (q *Queue) enqueue(ctx context.Context, verb string, args any, objectKey string, sealed bool) (EnqueueResult, error) {
	var result EnqueueResult
	m, ok := MetadataFrom(ctx)
	if !ok || m.CreatedBy == "" {
		return result, rpcerr.Errf("forbidden", "trusted job identity required")
	}
	if verb == "" {
		return result, rpcerr.Errf("invalid_argument", "verb required")
	}
	raw, e := json.Marshal(args)
	if e != nil {
		if sealed {
			return result, errors.New("jobs: input encoding failed")
		}
		return result, e
	}
	jid, e := id.New()
	if e != nil {
		return result, e
	}
	if sealed {
		defer clear(raw)
		raw, e = q.sealInput(jid, verb, objectKey, raw)
		if e != nil {
			return result, e
		}
	}
	var token [32]byte
	if _, e = rand.Read(token[:]); e != nil {
		return result, e
	}
	text := base64.RawURLEncoding.EncodeToString(token[:])
	now := q.clock.Now()
	j := Job{ID: jid, Verb: verb, Args: raw, ObjectKey: objectKey, Actor: m.Actor, Role: m.Role, CreatedBy: m.CreatedBy, Scopes: m.Scopes, Automation: m.Automation, Effectful: m.Effectful, State: "queued", Created: now, Updated: now}
	if e = q.healthy(); e != nil {
		return result, e
	}
	claim := store.JobClaim{JobID: j.ID, ClaimHash: claimHash(text), Expires: 0}
	// Persist both rows atomically so a claim failure cannot orphan a queued job.
	if e = q.store.CreateJobWithClaim(ctx, j, claim); e != nil {
		return result, e
	}
	if e = q.record(j, "ok", "", "enqueue"); e != nil {
		return result, e
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return EnqueueResult{Job: j, ClaimToken: text}, nil
}
func claimHash(s string) string                                      { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (q *Queue) Status(ctx context.Context, jid string) (Job, error) { return q.store.GetJob(ctx, jid) }
func (q *Queue) List(ctx context.Context, f store.JobFilter) ([]Job, error) {
	return q.store.ListJobs(ctx, f)
}
func (q *Queue) PublishClaim(ctx context.Context, jid string, fields map[string]any) error {
	if e := q.healthy(); e != nil {
		return e
	}
	if q.sealer == nil {
		return errors.New("jobs: sealer not configured")
	}
	raw, e := json.Marshal(fields)
	if e != nil {
		return e
	}
	s, e := q.sealer.Seal(raw)
	if e != nil {
		return e
	}
	return q.store.PublishClaim(ctx, jid, s.Nonce, s.Ciphertext)
}
func (q *Queue) Claim(ctx context.Context, jid, token string) (map[string]any, error) {
	if e := q.healthy(); e != nil {
		return nil, e
	}
	m, ok := MetadataFrom(ctx)
	if !ok || m.CreatedBy == "" {
		return nil, rpcerr.Errf("forbidden", "claim owner required")
	}
	if q.sealer == nil {
		return nil, errors.New("jobs: sealer not configured")
	}
	c, e := q.store.ConsumeClaim(ctx, jid, claimHash(token), m.CreatedBy)
	if e != nil {
		return nil, e
	}
	raw, e := q.sealer.Open(secret.Sealed{Nonce: c.Nonce, Ciphertext: c.Ciphertext})
	if e != nil {
		return nil, e
	}
	var fields map[string]any
	e = json.Unmarshal(raw, &fields)
	return fields, e
}

func (q *Queue) healthy() error {
	if q.audit == nil {
		return errors.New("jobs: audit not configured")
	}
	return q.audit.Healthy()
}

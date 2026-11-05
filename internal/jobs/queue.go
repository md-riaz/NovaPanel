package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
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
	store           Persistence
	sealer          Sealer
	audit           Auditor
	clock           Clock
	mu              sync.Mutex
	runner          func(context.Context, *Job) error
	attemptDeadline func(string) time.Duration
	running         bool
	startup         func(context.Context) (*Job, error)
	resume          func(context.Context, *Job) error
	postRecovery    func(context.Context) error
	wake            chan struct{}
	locks           keyedLock
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
func (q *Queue) SetAttemptDeadline(deadline func(string) time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.attemptDeadline = deadline
}
func (q *Queue) record(j Job, outcome, code, stage string) error {
	if q.audit == nil {
		return errors.New("jobs: audit not configured")
	}
	if e := q.audit.Healthy(); e != nil {
		return e
	}
	return q.audit.Record(audit.Event{Principal: audit.JobPrincipal(j.CreatedBy, j.Role, j.Scopes), Actor: j.Actor, Role: j.Role, Verb: j.Verb, ObjectKey: j.ObjectKey, Outcome: outcome, Code: code, Detail: map[string]any{"job_id": j.ID, "stage": stage}})
}
func (q *Queue) Enqueue(ctx context.Context, verb string, args any, objectKey string) (EnqueueResult, error) {
	return q.enqueue(ctx, verb, args, objectKey, false, nil)
}

type databaseIntent struct {
	password string
	database *store.Database
	user     *store.DatabaseUser
}

// Secondary locks are acquired after the parent database job lock.
func (q *Queue) LockDatabaseName(ctx context.Context, name string) (func(), error) {
	return q.locks.lock(ctx, "dbname:"+name)
}

func (q *Queue) LockDatabaseUser(ctx context.Context, username string) (func(), error) {
	return q.locks.lock(ctx, "dbuser:"+username)
}

func (q *Queue) EnqueueDatabase(ctx context.Context, verb string, args any, objectKey, password string, database *store.Database, user *store.DatabaseUser) (EnqueueResult, error) {
	return q.enqueue(ctx, verb, args, objectKey, false, &databaseIntent{password, database, user})
}

type idempotencyKey struct{}
type idempotencyRequest struct{ key, hash string }

// WithIdempotency is called by trusted RPC dispatch after authentication.
func WithIdempotency(ctx context.Context, key, hash string) context.Context {
	return context.WithValue(ctx, idempotencyKey{}, idempotencyRequest{key, hash})
}

// LockIdempotency serializes dispatch through admission, including handler preconditions.
func (q *Queue) LockIdempotency(ctx context.Context) (func(), error) {
	m, ok := MetadataFrom(ctx)
	r, _ := ctx.Value(idempotencyKey{}).(idempotencyRequest)
	if !ok || m.CreatedBy == "" || r.key == "" {
		return nil, rpcerr.Errf("forbidden", "trusted job identity required")
	}
	return q.locks.lock(ctx, "idempotency:"+claimHash(m.CreatedBy+"\x00"+r.key))
}

func (q *Queue) Replay(ctx context.Context) (EnqueueResult, bool, error) {
	m, ok := MetadataFrom(ctx)
	r, _ := ctx.Value(idempotencyKey{}).(idempotencyRequest)
	if !ok || m.CreatedBy == "" || r.key == "" {
		return EnqueueResult{}, false, rpcerr.Errf("forbidden", "trusted job identity required")
	}
	if err := q.healthy(); err != nil {
		return EnqueueResult{}, false, err
	}
	p, ok := q.store.(interface {
		ReplayJob(context.Context, string, string, string) (store.Job, []byte, bool, error)
	})
	if !ok || q.sealer == nil {
		return EnqueueResult{}, false, errors.New("jobs: idempotency unavailable")
	}
	j, saved, found, err := p.ReplayJob(ctx, m.CreatedBy, claimHash(m.CreatedBy+"\x00"+r.key), r.hash)
	if errors.Is(err, store.ErrIdempotencyConflict) {
		return EnqueueResult{}, false, rpcerr.Errf("conflict", "Idempotency key reused")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return EnqueueResult{}, false, rpcerr.Errf("not_found", "Claim unavailable")
	}
	if err != nil || !found {
		return EnqueueResult{}, found, err
	}
	result, err := q.replayResult(j, saved)
	return result, true, err
}

func (q *Queue) replayResult(j Job, saved []byte) (EnqueueResult, error) {
	if len(saved) == 0 {
		return EnqueueResult{Job: j}, nil
	}
	var sealed secret.Sealed
	if json.Unmarshal(saved, &sealed) != nil || q.sealer == nil {
		return EnqueueResult{}, errors.New("jobs: invalid saved claim")
	}
	plain, err := q.sealer.Open(sealed)
	if err != nil {
		return EnqueueResult{}, errors.New("jobs: saved claim unavailable")
	}
	defer clear(plain)
	var binding struct{ JobID, Owner, Token string }
	if json.Unmarshal(plain, &binding) != nil || binding.JobID != j.ID || binding.Owner != j.CreatedBy || binding.Token == "" {
		return EnqueueResult{}, errors.New("jobs: saved claim binding mismatch")
	}
	return EnqueueResult{Job: j, ClaimToken: binding.Token}, nil
}

func (q *Queue) enqueue(ctx context.Context, verb string, args any, objectKey string, sealed bool, intent *databaseIntent) (EnqueueResult, error) {
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
	var nonce, ciphertext []byte
	if intent != nil {
		if q.sealer == nil || intent.password == "" {
			return result, errors.New("jobs: database intent unavailable")
		}
		payload, err := json.Marshal(inputPayload{j.ID, j.Verb, j.ObjectKey, json.RawMessage(raw)})
		if err != nil {
			return result, errors.New("jobs: input encoding failed")
		}
		// Bind the secret to the exact public intent as well as its job identity.
		secretPayload, err := json.Marshal(struct {
			Binding  json.RawMessage `json:"binding"`
			Password string          `json:"password"`
		}{payload, intent.password})
		clear(payload)
		if err != nil {
			return result, errors.New("jobs: input encoding failed")
		}
		sealedSecret, err := q.sealer.Seal(secretPayload)
		clear(secretPayload)
		if err != nil {
			return result, errors.New("jobs: input sealing failed")
		}
		nonce, ciphertext = sealedSecret.Nonce, sealedSecret.Ciphertext
	}
	request, _ := ctx.Value(idempotencyKey{}).(idempotencyRequest)
	p, ok := q.store.(interface {
		AdmitJob(context.Context, store.Job, store.JobClaim, string, string, []byte, []byte, []byte) (store.Job, []byte, bool, error)
	})
	if !ok {
		return result, errors.New("jobs: atomic admission unavailable")
	}
	var sealedToken []byte
	key := ""
	if request.key != "" {
		if q.sealer == nil || request.hash == "" {
			return result, errors.New("jobs: idempotency unavailable")
		}
		key = claimHash(m.CreatedBy + "\x00" + request.key)
		binding, err := json.Marshal(struct{ JobID, Owner, Token string }{j.ID, m.CreatedBy, text})
		if err != nil {
			return result, err
		}
		s, err := q.sealer.Seal(binding)
		clear(binding)
		if err != nil {
			return result, errors.New("jobs: claim sealing failed")
		}
		sealedToken, e = json.Marshal(s)
		if e != nil {
			return result, e
		}
	}
	existing, saved, replay, e := p.AdmitJob(ctx, j, claim, key, request.hash, sealedToken, nonce, ciphertext)
	if errors.Is(e, store.ErrIdempotencyConflict) {
		return result, rpcerr.Errf("conflict", "Idempotency key reused")
	}
	if errors.Is(e, sql.ErrNoRows) {
		return result, rpcerr.Errf("not_found", "Claim unavailable")
	}
	if e != nil {
		return result, e
	}
	if replay {
		return q.replayResult(existing, saved)
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	if e = q.record(j, "ok", "", "enqueue"); e != nil {
		return result, e
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
	defer clear(raw)
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
	defer clear(raw)
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

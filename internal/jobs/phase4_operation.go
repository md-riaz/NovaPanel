package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"novapanel/internal/id"
	"novapanel/internal/rpcerr"
	"novapanel/internal/secret"
	"novapanel/internal/store"
)

type operationStore interface {
	PrepareOperation(context.Context, store.Job, store.JobClaim, store.OperationRequest, []byte, []byte) (store.Operation, error)
	LoadOperation(context.Context, string) (store.Operation, error)
	CheckOperation(context.Context, string) error
	CompleteOperation(context.Context, string, *store.JobClaim) error
	FailOperation(context.Context, string, string) error
}

// EnqueueOperation is agent-only admission. It allocates IDs before any locks,
// seals credentials before SQL, and persists only nonsecret job arguments.
func (q *Queue) EnqueueOperation(ctx context.Context, r store.OperationRequest, fields map[string]any) (EnqueueResult, error) {
	var out EnqueueResult
	s, ok := q.store.(operationStore)
	if !ok {
		return out, errors.New("jobs: operation persistence unavailable")
	}
	m, ok := MetadataFrom(ctx)
	if !ok || m.CreatedBy == "" {
		return out, rpcerr.Errf("forbidden", "trusted job identity required")
	}
	if e := q.healthy(); e != nil {
		return out, e
	}
	verbs := map[store.OperationKind]string{store.DatabaseCreate: "database.create", store.DatabaseRotate: "database.rotate_password", store.DBUserCreate: "dbuser.create", store.DatabaseDelete: "database.delete", store.DBUserDelete: "dbuser.delete"}
	verb := verbs[r.Kind]
	if verb == "" {
		return out, rpcerr.Errf("invalid_argument", "operation kind unavailable")
	}
	var e error
	if r.Kind == store.DatabaseCreate {
		r.DatabaseID, e = id.New()
		if e != nil {
			return out, e
		}
	}
	if r.Kind == store.DatabaseCreate || r.Kind == store.DBUserCreate {
		r.UserID, e = id.New()
		if e != nil {
			return out, e
		}
	}
	jid, e := id.New()
	if e != nil {
		return out, e
	}
	var token [32]byte
	if _, e = rand.Read(token[:]); e != nil {
		return out, e
	}
	text := base64.RawURLEncoding.EncodeToString(token[:])
	j := Job{ID: jid, Verb: verb, ObjectKey: "db:" + r.DatabaseID, Args: json.RawMessage(`{}`), CreatedBy: m.CreatedBy, Role: m.Role, Actor: m.Actor, Scopes: m.Scopes, Automation: m.Automation, State: "queued", Created: q.clock.Now(), Updated: q.clock.Now()}
	var nonce, ciphertext []byte
	needsSecret := r.Kind == store.DatabaseCreate || r.Kind == store.DatabaseRotate || r.Kind == store.DBUserCreate
	if needsSecret {
		if fields == nil || q.sealer == nil {
			return out, errors.New("jobs: sealed operation credential required")
		}
		raw, e := json.Marshal(fields)
		if e != nil {
			return out, errors.New("jobs: operation encoding failed")
		}
		defer clear(raw)
		sealed, e := q.sealer.Seal(raw)
		if e != nil {
			return out, e
		}
		nonce, ciphertext = sealed.Nonce, sealed.Ciphertext
	} else if fields != nil {
		return out, errors.New("jobs: deletion cannot carry credentials")
	}
	if _, e = s.PrepareOperation(ctx, j, store.JobClaim{JobID: jid, ClaimHash: claimHash(text)}, r, nonce, ciphertext); e != nil {
		return out, e
	}
	if e = q.record(j, "ok", "", "enqueue"); e != nil {
		return out, e
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	if !needsSecret {
		text = ""
	}
	return EnqueueResult{Job: j, ClaimToken: text}, nil
}
func (q *Queue) operationKeys(ctx context.Context, j Job) ([]string, error) {
	s, ok := q.store.(operationStore)
	if !ok {
		return []string{j.ObjectKey}, nil
	}
	op, e := s.LoadOperation(ctx, j.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return []string{j.ObjectKey}, nil
	}
	if e != nil {
		return nil, e
	}
	return op.Keys(), nil
}

// operationTerminal runs only after the queue has recorded completion audit.
func (q *Queue) operationTerminal(ctx context.Context, j Job) (bool, error) {
	s, ok := q.store.(operationStore)
	if !ok {
		return false, nil
	}
	op, e := s.LoadOperation(ctx, j.ID)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return true, e
	}
	if j.State != "done" {
		return true, s.FailOperation(ctx, j.ID, j.Code)
	}
	var claim *store.JobClaim
	if len(op.Ciphertext) > 0 {
		raw, e := q.sealer.Open(secret.Sealed{Nonce: op.Nonce, Ciphertext: op.Ciphertext})
		if e != nil {
			return true, e
		}
		defer clear(raw)
		sealed, e := q.sealer.Seal(raw)
		if e != nil {
			return true, e
		}
		claim = &store.JobClaim{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}
	}
	return true, s.CompleteOperation(ctx, j.ID, claim)
}

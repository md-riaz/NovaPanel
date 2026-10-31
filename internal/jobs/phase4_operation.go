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
	"novapanel/internal/store"
)

func operationError(err error) error {
	switch {
	case errors.Is(err, store.ErrOperationExpired):
		return rpcerr.Errf("interrupted", "operation recovery expired")
	case errors.Is(err, store.ErrOperationLocked):
		return rpcerr.Errf("locked", "operation locked")
	case errors.Is(err, store.ErrSitePrecondition):
		return rpcerr.Errf("precondition_failed", "operation precondition failed")
	case errors.Is(err, store.ErrSiteConflict):
		return rpcerr.Errf("conflict", "operation membership changed")
	case errors.Is(err, store.ErrSiteInvalid):
		return rpcerr.Errf("invalid_argument", "invalid operation")
	case errors.Is(err, sql.ErrNoRows):
		return rpcerr.Errf("not_found", "operation target not found")
	}
	return err
}

// OperationCredentials is the complete credential-only DB recovery contract.
// WordPress configuration and salts require a separate typed journal.
type OperationCredentials struct {
	Password string `json:"password"`
}

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
	return q.enqueueOperation(ctx, r, fields, nil)
}
func (q *Queue) enqueueOperation(ctx context.Context, r store.OperationRequest, fields map[string]any, wp *WordPressCredentials) (EnqueueResult, error) {
	var out EnqueueResult
	if q.admission != nil {
		if err := q.admission(); err != nil {
			return out, err
		}
	}
	q.operationMu.Lock()
	defer q.operationMu.Unlock()
	if !q.operationsReady {
		return out, ErrRecoveryBlocked
	}
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
	verbs := map[store.OperationKind]string{store.WordPressInstall: "site.create", store.SiteDelete: "site.delete", store.DatabaseCreate: "database.create", store.DatabaseRotate: "database.rotate_password", store.DBUserCreate: "dbuser.create", store.DatabaseDelete: "database.delete", store.DBUserDelete: "dbuser.delete"}
	verb := verbs[r.Kind]
	if verb == "" {
		return out, rpcerr.Errf("invalid_argument", "operation kind unavailable")
	}
	var e error
	if r.Kind == store.WordPressInstall {
		if r.WordPress == nil || wp == nil {
			return out, errors.New("jobs: WordPress recovery input required")
		}
		r.SiteID, e = id.New()
		if e != nil {
			return out, e
		}
		w := *r.WordPress
		w.DomainID, e = id.New()
		if e != nil {
			return out, e
		}
		r.WordPress = &w
	}
	if r.Kind == store.DatabaseCreate || r.Kind == store.WordPressInstall {
		r.DatabaseID, e = id.New()
		if e != nil {
			return out, e
		}
	}
	if r.Kind == store.DatabaseCreate || r.Kind == store.DBUserCreate || r.Kind == store.WordPressInstall {
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
	if r.Kind == store.SiteDelete {
		j.ObjectKey = "site:" + r.SiteID
	}
	if r.Kind == store.WordPressInstall {
		j.ObjectKey = "site:" + r.WordPress.Input.SiteName
		r.WordPress.Input.DatabaseID = r.DatabaseID
		r.WordPress.Input.DatabaseUserID = r.UserID
	}
	j.Effectful = m.Effectful
	var nonce, ciphertext []byte
	needsSecret := r.Kind == store.DatabaseCreate || r.Kind == store.DatabaseRotate || r.Kind == store.DBUserCreate || r.Kind == store.WordPressInstall
	if r.Kind == store.WordPressInstall {
		if wp == nil || q.sealer == nil || fields != nil || !validWordPressCredentials(*wp) {
			return out, errors.New("jobs: WordPress recovery input required")
		}
		raw, err := json.Marshal(wordPressInput{Version: 1, JobID: jid, SiteID: r.SiteID, DatabaseID: r.DatabaseID, Owner: m.CreatedBy, Authority: metadata(j), Reservation: *r.WordPress, Credentials: *wp})
		if err != nil {
			return out, err
		}
		defer clear(raw)
		sealed, err := q.sealer.Seal(raw)
		if err != nil {
			return out, err
		}
		nonce, ciphertext = sealed.Nonce, sealed.Ciphertext
	} else if needsSecret {
		if fields == nil || q.sealer == nil {
			return out, errors.New("jobs: sealed operation credential required")
		}
		if len(fields) != 1 {
			return out, errors.New("jobs: credential-only recovery input required")
		}
		password, ok := fields["password"].(string)
		if !ok || password == "" {
			return out, errors.New("jobs: operation password required")
		}
		raw, e := json.Marshal(operationInput{Version: 1, JobID: jid, Kind: r.Kind, DatabaseID: r.DatabaseID, Owner: m.CreatedBy, Authority: metadata(j), Credentials: OperationCredentials{Password: password}})
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
	// Discover and snapshot attached identities while holding the same site lock
	// used by attachment and the worker. The worker reacquires the full durable set.
	if r.Kind == store.SiteDelete {
		release := q.locks.lock("site:" + r.SiteID)
		defer release()
	}
	if _, e = s.PrepareOperation(ctx, j, store.JobClaim{JobID: jid, ClaimHash: claimHash(text)}, r, nonce, ciphertext); e != nil {
		return out, operationError(e)
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

func (q *Queue) expireOperations(ctx context.Context) error {
	s, ok := q.store.(interface {
		ExpireOperations(context.Context) (int, error)
	})
	if !ok {
		return nil
	}
	if e := q.healthy(); e != nil {
		return e
	}
	_, e := s.ExpireOperations(ctx)
	return e
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
		if op.Kind == store.WordPressInstall {
			payload, err := q.OpenWordPressCredentials(ctx, j.ID)
			if err != nil {
				return true, err
			}
			raw, err := json.Marshal(wordPressClaim(op, payload))
			if err != nil {
				return true, err
			}
			defer clear(raw)
			sealed, err := q.sealer.Seal(raw)
			if err != nil {
				return true, err
			}
			return true, s.CompleteOperation(ctx, j.ID, &store.JobClaim{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext})
		}
		payload, e := q.OpenOperationCredentials(ctx, j.ID)
		if e != nil {
			return true, e
		}
		projection, e := json.Marshal(payload)
		if e != nil {
			return true, e
		}
		defer clear(projection)
		sealed, e := q.sealer.Seal(projection)
		if e != nil {
			return true, e
		}
		claim = &store.JobClaim{Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext}
	}
	return true, s.CompleteOperation(ctx, j.ID, claim)
}

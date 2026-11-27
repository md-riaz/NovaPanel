package jobs

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"novapanel/internal/id"
	"novapanel/internal/store"
)

// EnqueueFreshWordPress binds the job, identity and sealed input in one transaction.
func (q *Queue) EnqueueFreshWordPress(ctx context.Context, r store.WordPressReservation, site store.Site, domain store.Domain, limits store.PHPLimits, credentials WordPressCredentials) (EnqueueResult, error) {
	var result EnqueueResult
	if q.admission != nil {
		if err := q.admission(); err != nil {
			return result, err
		}
	}
	ctx, release, err := q.AdmitMutation(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	if !q.operationsReady {
		return result, ErrRecoveryBlocked
	}
	if err = q.healthy(); err != nil {
		return result, err
	}
	m, ok := MetadataFrom(ctx)
	if !ok || m.CreatedBy == "" || q.sealer == nil || !validWordPressCredentials(credentials) {
		return result, errors.New("jobs: WordPress admission unavailable")
	}
	s, ok := q.store.(interface {
		PrepareFreshWordPress(context.Context, store.Job, store.JobClaim, store.WordPressReservation, store.Site, store.Domain, store.PHPLimits, func(store.WordPressReservation) ([]byte, []byte, error), ...store.FreshWordPressIdempotency) (store.Operation, error)
	})
	if !ok {
		return result, errors.New("jobs: WordPress admission unavailable")
	}
	r.Input.DatabaseID, err = id.New()
	if err != nil {
		return result, err
	}
	r.Input.DatabaseUserID, err = id.New()
	if err != nil {
		return result, err
	}
	jid, err := id.New()
	if err != nil {
		return result, err
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		return result, err
	}
	claimToken := base64.RawURLEncoding.EncodeToString(token[:])
	args, err := json.Marshal(map[string]string{"site_id": r.SiteID})
	if err != nil {
		return result, err
	}
	j := Job{ID: jid, Verb: "site.create", ObjectKey: "site:" + r.SiteID, Args: args, CreatedBy: m.CreatedBy, Role: m.Role, Actor: m.Actor, Scopes: m.Scopes, Automation: m.Automation, Effectful: m.Effectful, State: "queued", Created: q.clock.Now(), Updated: q.clock.Now()}
	request, _ := ctx.Value(idempotencyKey{}).(idempotencyRequest)
	var idem store.FreshWordPressIdempotency
	if request.key != "" {
		if request.hash == "" {
			return result, errors.New("jobs: idempotency unavailable")
		}
		idem.Key, idem.Payload = claimHash(m.CreatedBy+"\x00"+request.key), request.hash
		binding, e := json.Marshal(struct{ JobID, Owner, Token string }{jid, m.CreatedBy, claimToken})
		if e != nil {
			return result, e
		}
		sealed, e := q.sealer.Seal(binding)
		clear(binding)
		if e != nil {
			return result, e
		}
		idem.SealedToken, e = json.Marshal(sealed)
		if e != nil {
			return result, e
		}
	}
	unlock, err := q.LockSiteOSUser(ctx, r.SiteID, r.Input.SiteName)
	if err != nil {
		return result, err
	}
	defer unlock()
	_, err = s.PrepareFreshWordPress(ctx, j, store.JobClaim{JobID: jid, ClaimHash: claimHash(claimToken)}, r, site, domain, limits, func(reservation store.WordPressReservation) ([]byte, []byte, error) {
		raw, err := json.Marshal(wordPressInput{Version: 1, JobID: jid, SiteID: r.SiteID, DatabaseID: r.Input.DatabaseID, Owner: m.CreatedBy, Authority: metadata(j), Reservation: reservation, Credentials: credentials})
		if err != nil {
			return nil, nil, err
		}
		defer clear(raw)
		sealed, err := q.sealer.Seal(raw)
		return sealed.Nonce, sealed.Ciphertext, err
	}, idem)
	if err != nil {
		return result, operationError(err)
	}
	if err = q.record(j, "ok", "", "enqueue"); err != nil {
		return result, err
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return EnqueueResult{Job: j, ClaimToken: claimToken, ClaimState: "live"}, nil
}

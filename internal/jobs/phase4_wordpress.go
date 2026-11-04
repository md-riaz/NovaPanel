package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"reflect"
)

// WordPressCredentials is execution-only. Salts never enter the delivered claim.
// The trusted admitting agent generates these independently before admission.
type WordPressCredentials struct {
	DatabasePassword string    `json:"database_password"`
	AdminPassword    string    `json:"admin_password"`
	Salts            [8]string `json:"salts"`
}

func validWordPressCredentials(c WordPressCredentials) bool {
	if c.DatabasePassword == "" || c.AdminPassword == "" {
		return false
	}
	for _, s := range c.Salts {
		if s == "" {
			return false
		}
	}
	return true
}

type wordPressInput struct {
	Version                          int
	JobID, SiteID, DatabaseID, Owner string
	Authority                        Metadata
	Credentials                      WordPressCredentials
	Reservation                      store.WordPressReservation
}

func (q *Queue) EnqueueWordPress(ctx context.Context, r store.WordPressReservation, c WordPressCredentials) (EnqueueResult, error) {
	return q.enqueueOperation(ctx, store.OperationRequest{Kind: store.WordPressInstall, WordPress: &r}, nil, &c)
}
func (q *Queue) OpenWordPressCredentials(ctx context.Context, jid string) (WordPressCredentials, error) {
	var zero WordPressCredentials
	refuse := errors.New("jobs: WordPress recovery input unavailable")
	s, ok := q.store.(operationStore)
	if !ok || q.sealer == nil {
		return zero, refuse
	}
	if e := s.CheckOperation(ctx, jid); e != nil {
		return zero, e
	}
	op, e := s.LoadOperation(ctx, jid)
	if e != nil {
		return zero, e
	}
	if op.Kind != store.WordPressInstall {
		return zero, refuse
	}
	raw, e := q.sealer.Open(secret.Sealed{Nonce: op.Nonce, Ciphertext: op.Ciphertext})
	if e != nil {
		return zero, refuse
	}
	defer clear(raw)
	var input wordPressInput
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		return zero, refuse
	}
	var extra any
	if d.Decode(&extra) != io.EOF || input.Version != 1 || input.JobID != jid || input.SiteID != op.SiteID || input.DatabaseID != op.DatabaseID || input.Owner != op.Owner || !reflect.DeepEqual(input.Reservation, *op.Identity.WordPress) || !validWordPressCredentials(input.Credentials) {
		return zero, refuse
	}
	j, e := q.store.GetJob(ctx, jid)
	if e != nil || !sameOperationAuthority(input.Authority, metadata(j)) {
		return zero, refuse
	}
	return input.Credentials, nil
}
func wordPressClaim(op store.Operation, c WordPressCredentials) map[string]any {
	db := op.Identity.Database
	a := op.Identity.Accounts[0]
	admin := op.Identity.WordPress.Input.Administrator
	return map[string]any{"database": map[string]string{"db_id": db.ID, "name": db.Name, "username": a.Username, "host": a.Host, "password": c.DatabasePassword}, "wordpress": map[string]string{"admin_username": admin.Username, "admin_email": admin.Email, "admin_password": c.AdminPassword}}
}

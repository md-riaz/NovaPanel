package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"novapanel/internal/secret"
	"novapanel/internal/store"
	"slices"
)

func sameOperationAuthority(a, b Metadata) bool {
	return a.Actor == b.Actor && a.Role == b.Role && a.CreatedBy == b.CreatedBy && a.Automation == b.Automation && a.Effectful == b.Effectful && slices.Equal(a.Scopes, b.Scopes)
}

type operationInput struct {
	Version     int                  `json:"version"`
	JobID       string               `json:"job_id"`
	Kind        store.OperationKind  `json:"kind"`
	DatabaseID  string               `json:"db_id"`
	Owner       string               `json:"owner"`
	Authority   Metadata             `json:"authority"`
	Credentials OperationCredentials `json:"credentials"`
}

// OpenOperationCredentials opens only this operation's immutable credential.
// Call under the operation lock set, before each external effect, never persist it.
func (q *Queue) OpenOperationCredentials(ctx context.Context, jid string) (OperationCredentials, error) {
	var out OperationCredentials
	refuse := errors.New("jobs: operation credential unavailable")
	s, ok := q.store.(operationStore)
	if !ok || q.sealer == nil {
		return out, refuse
	}
	if err := s.CheckOperation(ctx, jid); err != nil {
		return out, err
	}
	op, err := s.LoadOperation(ctx, jid)
	if err != nil {
		return out, err
	}
	raw, err := q.sealer.Open(secret.Sealed{Nonce: op.Nonce, Ciphertext: op.Ciphertext})
	if err != nil {
		return out, refuse
	}
	defer clear(raw)
	var input operationInput
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		return out, refuse
	}
	var extra any
	if d.Decode(&extra) != io.EOF || input.Version != 1 || input.JobID != jid || input.Kind != op.Kind || input.DatabaseID != op.DatabaseID || input.Owner != op.Owner || input.Credentials.Password == "" {
		return out, refuse
	}
	j, err := q.store.GetJob(ctx, jid)
	if err != nil || !sameOperationAuthority(input.Authority, metadata(j)) {
		return out, refuse
	}
	return input.Credentials, nil
}

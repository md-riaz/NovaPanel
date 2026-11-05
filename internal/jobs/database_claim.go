package jobs

import (
	"context"
	"encoding/json"
	"errors"
)

// PublishDatabaseClaim seals the secret before atomically publishing it with the job checkpoint.
func (q *Queue) PublishDatabaseClaim(ctx context.Context, j *Job, result json.RawMessage, fields map[string]any) error {
	if err := q.healthy(); err != nil {
		return err
	}
	if j == nil || q.sealer == nil {
		return errors.New("jobs: database claim unavailable")
	}
	s, ok := q.store.(interface {
		CommitDatabaseClaim(context.Context, string, string, string, json.RawMessage, []byte, []byte) error
	})
	if !ok {
		return errors.New("jobs: database claim store unavailable")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return errors.New("jobs: claim encoding failed")
	}
	defer clear(raw)
	sealed, err := q.sealer.Seal(raw)
	if err != nil {
		return errors.New("jobs: claim sealing failed")
	}
	if err := s.CommitDatabaseClaim(ctx, j.ID, j.Verb, j.ObjectKey, result, sealed.Nonce, sealed.Ciphertext); err != nil {
		return err
	}
	j.Step = "database_committed"
	j.Result = append(json.RawMessage(nil), result...)
	return nil
}

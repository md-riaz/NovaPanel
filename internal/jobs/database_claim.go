package jobs

import (
	"context"
	"encoding/json"
	"errors"
)

// PublishDatabaseClaim seals the secret before atomically publishing it with the job checkpoint.
func (q *Queue) PublishDatabaseClaim(ctx context.Context, j *Job, result json.RawMessage, fields map[string]any) error {
	return q.publishDatabaseClaim(ctx, j, result, fields, "")
}

// PublishRotateConflictClaim binds a recoverable claim to its operator failure.
func (q *Queue) PublishRotateConflictClaim(ctx context.Context, j *Job, fields map[string]any, code string) error {
	if code != "conflict" && code != "precondition_failed" {
		return errors.New("jobs: invalid rotation outcome")
	}
	return q.publishDatabaseClaim(ctx, j, json.RawMessage(`{}`), fields, code)
}

func (q *Queue) publishDatabaseClaim(ctx context.Context, j *Job, result json.RawMessage, fields map[string]any, code string) error {
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
	if code != "" {
		c, ok := q.store.(interface {
			CommitRotateConflictClaim(context.Context, string, string, string, json.RawMessage, []byte, []byte, string) error
		})
		if !ok {
			return errors.New("jobs: rotation outcome store unavailable")
		}
		err = c.CommitRotateConflictClaim(ctx, j.ID, j.Verb, j.ObjectKey, result, sealed.Nonce, sealed.Ciphertext, code)
	} else {
		err = s.CommitDatabaseClaim(ctx, j.ID, j.Verb, j.ObjectKey, result, sealed.Nonce, sealed.Ciphertext)
	}
	if err != nil {
		return err
	}
	j.Code = code
	j.Step = "database_committed"
	j.Result = append(json.RawMessage(nil), result...)
	return nil
}

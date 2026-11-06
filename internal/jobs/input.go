package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"novapanel/internal/secret"
)

// EnqueueSealed seals the complete args before any durable insertion. Trusted
// handlers still own validation, authorization and persisted object-key resolution.
func (q *Queue) EnqueueSealed(ctx context.Context, verb string, args any, objectKey string) (EnqueueResult, error) {
	return q.enqueue(ctx, verb, args, objectKey, true, nil)
}

type inputEnvelope struct {
	Version int           `json:"_sealed_job_input"`
	Sealed  secret.Sealed `json:"sealed"`
}
type inputPayload struct {
	ID        string          `json:"id"`
	Verb      string          `json:"verb"`
	ObjectKey string          `json:"object_key"`
	Args      json.RawMessage `json:"args"`
}

func (q *Queue) sealInput(id, verb, objectKey string, args []byte) ([]byte, error) {
	if q.sealer == nil {
		return nil, errors.New("jobs: input sealer not configured")
	}
	raw, err := json.Marshal(inputPayload{id, verb, objectKey, args})
	if err != nil {
		return nil, errors.New("jobs: input encoding failed")
	}
	defer clear(raw)
	sealed, err := q.sealer.Seal(raw)
	if err != nil {
		return nil, errors.New("jobs: input sealing failed")
	}
	return json.Marshal(inputEnvelope{1, sealed})
}

// OpenInput returns caller-owned plaintext only for a bound sealed job. Exec must
// clear the returned bytes, revalidate them, and never assign them to Job fields.
func (q *Queue) OpenInput(j *Job) ([]byte, error) {
	refuse := errors.New("jobs: sealed input unavailable")
	if j == nil || q.sealer == nil {
		return nil, refuse
	}
	var envelope inputEnvelope
	if json.Unmarshal(j.Args, &envelope) != nil || envelope.Version != 1 {
		p, ok := q.store.(interface {
			DatabaseIntentSecret(context.Context, string) ([]byte, []byte, error)
		})
		if !ok {
			return nil, refuse
		}
		nonce, ciphertext, err := p.DatabaseIntentSecret(context.Background(), j.ID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, ErrRetry
			}
			return nil, refuse
		}
		raw, err := q.sealer.Open(secret.Sealed{Nonce: nonce, Ciphertext: ciphertext})
		if err != nil {
			return nil, refuse
		}
		defer clear(raw)
		var payload struct {
			Binding  inputPayload `json:"binding"`
			Password string       `json:"password"`
		}
		if json.Unmarshal(raw, &payload) != nil || payload.Password == "" || payload.Binding.ID != j.ID || payload.Binding.Verb != j.Verb || payload.Binding.ObjectKey != j.ObjectKey || !bytes.Equal(payload.Binding.Args, j.Args) {
			return nil, refuse
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(j.Args, &fields) != nil || fields == nil {
			return nil, refuse
		}
		if _, exists := fields["password"]; exists {
			return nil, refuse
		}
		password, err := json.Marshal(payload.Password)
		if err != nil {
			return nil, refuse
		}
		fields["password"] = password
		return json.Marshal(fields)
	}
	raw, err := q.sealer.Open(envelope.Sealed)
	if err != nil {
		return nil, refuse
	}
	defer clear(raw)
	var payload inputPayload
	if json.Unmarshal(raw, &payload) != nil || payload.ID != j.ID || payload.Verb != j.Verb || payload.ObjectKey != j.ObjectKey {
		return nil, refuse
	}
	return payload.Args, nil
}

package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"novapanel/internal/store"
)

func TestSealedWorkerTerminalOutcomes(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "panic", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			q, s, a, ctx := fixture(t)
			const marker = "private-input-never-persist-this"
			r, err := q.EnqueueSealed(ctx, "ssl.upload", map[string]string{"key_pem": marker}, "site:one")
			if err != nil {
				t.Fatal(err)
			}
			runctx, cancel := context.WithCancel(ctx)
			defer cancel()
			q.SetRunner(func(ctx context.Context, j *Job) error {
				raw, err := q.OpenInput(j)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(raw)
				if !bytes.Contains(raw, []byte(marker)) {
					t.Fatal("missing opened input")
				}
				switch outcome {
				case "failure":
					return errors.New(marker)
				case "panic":
					panic(marker)
				case "cancel":
					cancel()
					return ctx.Err()
				}
				return nil
			})
			if err = q.execute(runctx, r.Job); err != nil {
				t.Fatal(err)
			}
			j, err := s.GetJob(ctx, r.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "cancel" {
				if j.State != "running" || !bytes.Equal(j.Args, r.Job.Args) {
					t.Fatal("shutdown destroyed retry input")
				}
				// Explicit terminal cancellation uses the existing failed state, not a new enum.
				j.State = "failed"
				if err = s.UpdateJob(ctx, j); err != nil {
					t.Fatal(err)
				}
				j, err = s.GetJob(ctx, j.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if string(j.Args) != "{}" {
				t.Fatal("terminal ciphertext remains")
			}
			events, err := s.JobEvents(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			visible, _ := json.Marshal(j)
			if bytes.Contains(visible, []byte(marker)) || bytes.Contains([]byte(fmt.Sprint(a.events, events)), []byte(marker)) {
				t.Fatal("plaintext escaped")
			}
		})
	}
}

func TestSealedInputBindingAndRefusals(t *testing.T) {
	q, s, _, ctx := fixture(t)
	r, err := q.EnqueueSealed(ctx, "ssl.upload", map[string]string{"key_pem": "private"}, "site:one")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Job){func(j *Job) { j.ID = "other" }, func(j *Job) { j.Verb = "other" }, func(j *Job) { j.ObjectKey = "other" }, func(j *Job) { j.Args = []byte(`{"_sealed_job_input":1,"sealed":{}}`) }} {
		j := r.Job
		change(&j)
		if _, err = q.OpenInput(&j); err == nil {
			t.Fatal("unbound input accepted")
		}
	}
	q.sealer = nil
	if _, err = q.EnqueueSealed(ctx, "ssl.upload", map[string]string{"key_pem": "private"}, "site:one"); err == nil {
		t.Fatal("missing sealer accepted")
	}
	if _, err = q.EnqueueSealed(context.Background(), "ssl.upload", nil, "site:one"); err == nil {
		t.Fatal("untrusted enqueue accepted")
	}
	rows, err := s.ListJobs(ctx, store.JobFilter{})
	if err != nil || len(rows) != 1 {
		t.Fatal("refusal inserted job", err)
	}
}

func TestSealedInputEncodingFailureIsRedacted(t *testing.T) {
	q, _, _, ctx := fixture(t)
	_, err := q.EnqueueSealed(ctx, "ssl.upload", secretMarshalFailure{}, "site:one")
	if err == nil || bytes.Contains([]byte(err.Error()), []byte("secret-marker")) {
		t.Fatal("encoding error leaked input")
	}
}

type secretMarshalFailure struct{}

func (secretMarshalFailure) MarshalJSON() ([]byte, error) { return nil, errors.New("secret-marker") }

func TestSealedEffectfulInterruptedRecoveryRetainsInput(t *testing.T) {
	q, s, _, ctx := fixture(t)
	m, _ := MetadataFrom(ctx)
	m.Effectful = true
	r, err := q.EnqueueSealed(WithMetadata(ctx, m), "test.effectful", map[string]string{"secret": "private"}, "system")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartJob(ctx, r.Job.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverJobs(ctx, 2); err != nil {
		t.Fatal(err)
	}
	j, err := s.GetJob(ctx, r.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != "failed" || j.Code != "interrupted" || !bytes.Equal(j.Args, r.Job.Args) {
		t.Fatal("operational interruption lost input")
	}
	if err = s.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, j.ID)
	if err != nil || !bytes.Equal(j.Args, r.Job.Args) {
		t.Fatal("interrupted update lost retry input", err)
	}
	j.Code = "agent_error"
	if err = s.UpdateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j, err = s.GetJob(ctx, j.ID)
	if err != nil || string(j.Args) != "{}" {
		t.Fatal("explicit terminal failure retained input", err)
	}
}

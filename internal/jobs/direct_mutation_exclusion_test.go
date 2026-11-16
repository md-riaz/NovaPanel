package jobs

import (
	"context"
	"errors"
	"novapanel/internal/store"
	"testing"
)

func TestDirectQueueMutationsRefuseFailedStartupWithoutConsumingClaim(t *testing.T) {
	q, _, _, ctx := fixture(t)
	en, err := q.Enqueue(ctx, "reconcile", nil, "system")
	if err != nil {
		t.Fatal(err)
	}
	if err = q.PublishClaim(ctx, en.Job.ID, map[string]any{"password": "secret"}); err != nil {
		t.Fatal(err)
	}
	fail := true
	q.SetStartupRecovery(func(context.Context) (*Job, error) {
		if fail {
			return nil, errors.New("unresolved TLS")
		}
		return nil, nil
	}, nil)
	if err = q.PrepareStartup(ctx); err == nil {
		t.Fatal("expected startup failure")
	}
	if _, err = q.AddDomain(ctx, store.Domain{}, ""); !errors.Is(err, ErrRecoveryBlocked) {
		t.Errorf("AddDomain: %v", err)
	}
	if err = q.PublishClaim(ctx, en.Job.ID, map[string]any{"password": "replacement"}); !errors.Is(err, ErrRecoveryBlocked) {
		t.Errorf("PublishClaim: %v", err)
	}
	fail = false
	if err = q.PrepareStartup(ctx); err != nil {
		t.Fatal(err)
	}
	fields, err := q.Claim(ctx, en.Job.ID, en.ClaimToken)
	if err != nil {
		t.Fatal(err)
	}
	if fields["password"] != "secret" {
		t.Fatalf("claim changed or consumed: %v", fields)
	}
}

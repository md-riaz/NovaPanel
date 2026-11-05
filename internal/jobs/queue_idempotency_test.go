package jobs

import "testing"

func TestEnqueueIdempotentReplay(t *testing.T) {
	q, _, _, ctx := fixture(t)
	ctx = WithIdempotency(ctx, "retry-key", "request-hash")
	a, err := q.Enqueue(ctx, "reconcile", map[string]string{"a": "b"}, "system")
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.Enqueue(ctx, "reconcile", map[string]string{"a": "b"}, "system")
	if err != nil {
		t.Fatal(err)
	}
	if a.Job.ID != b.Job.ID || a.ClaimToken != b.ClaimToken {
		t.Fatal("retry created a different job or claim")
	}
	if _, err = q.Enqueue(WithIdempotency(ctx, "retry-key", "different"), "reconcile", nil, "system"); err == nil {
		t.Fatal("payload conflict accepted")
	}
	other := WithMetadata(ctx, Metadata{CreatedBy: "other", Role: "owner"})
	other = WithIdempotency(other, "retry-key", "request-hash")
	c, err := q.Enqueue(other, "reconcile", nil, "system")
	if err != nil || c.Job.ID == a.Job.ID || c.ClaimToken == a.ClaimToken {
		t.Fatalf("owner isolation: %+v %v", c, err)
	}
	if _, err = q.Claim(other, a.Job.ID, a.ClaimToken); err == nil {
		t.Fatal("cross-owner claim accepted")
	}
	if err = q.PublishClaim(ctx, a.Job.ID, map[string]any{"password": "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Claim(ctx, a.Job.ID, a.ClaimToken); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Enqueue(ctx, "reconcile", nil, "system"); err == nil {
		t.Fatal("consumed claim replayed")
	}
}
